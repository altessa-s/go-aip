// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/projection"
)

// viewCustom is a custom named view above ViewFull.
const viewCustom = projection.ViewFull + 1

// viewPolicy builds a policy with BASIC and a custom view on top of the given options.
func viewPolicy(tb testing.TB, opts ...projection.PolicyOption) *projection.Policy {
	tb.Helper()

	return mustPolicy(tb, append([]projection.PolicyOption{
		projection.WithUntrustedInput(true),
		projection.WithAllowedFields("name", "email", "createTime", "address.*"),
		projection.WithView(projection.ViewBasic, "name", "email", "name"),
		projection.WithView(viewCustom, "address.city"),
	}, opts...)...)
}

var allFields = []string{"address", "createTime", "email", "name"}

// TestOmittedMaskSelectsAllFields pins the AIP-157 MUST: without a field
// mask every authorized field is returned, whatever views are configured.
func TestOmittedMaskSelectsAllFields(t *testing.T) {
	t.Parallel()

	for _, def := range []projection.View{projection.ViewFull, projection.ViewBasic, viewCustom} {
		p := viewPolicy(t, projection.WithDefaultView(def))

		for _, spec := range []projection.Spec{{}, {Mode: projection.ModeAll}} {
			f, err := p.Validate(spec)
			require.NoError(t, err)
			require.Equal(t, allFields, f.Paths(), "default view %v, spec %+v", def, spec)
		}
	}

	p := viewPolicy(t)
	require.Equal(t, projection.ViewFull, p.DefaultView(), "FULL is the default view")

	f, err := p.Select(projection.Spec{}, projection.ViewUnspecified)
	require.NoError(t, err)
	require.Equal(t, allFields, f.Paths(), "with the FULL default an unspecified view keeps every field")
}

func TestSelectRule(t *testing.T) {
	t.Parallel()

	p := viewPolicy(t, projection.WithDefaultView(projection.ViewBasic))

	tests := []struct {
		name string
		spec projection.Spec
		view projection.View
		want []string
	}{
		{name: "unspecified takes the default view", view: projection.ViewUnspecified, want: []string{"email", "name"}},
		{name: "basic", view: projection.ViewBasic, want: []string{"email", "name"}},
		{name: "full", view: projection.ViewFull, want: allFields},
		{name: "custom", view: viewCustom, want: []string{"address.city"}},
		{name: "star wins over the view", spec: projection.Spec{Mode: projection.ModeAll}, view: projection.ViewBasic, want: allFields},
		{name: "mask wins over the view", spec: projection.Spec{Paths: []string{"createTime"}}, view: projection.ViewBasic, want: []string{"createTime"}},
		{name: "mask wins over the default view", spec: projection.Spec{Paths: []string{"createTime"}}, want: []string{"createTime"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := p.Select(tc.spec, tc.view)
			require.NoError(t, err)
			require.Equal(t, tc.want, f.Paths())
		})
	}

	none, err := p.Select(projection.Spec{Mode: projection.ModeNone}, projection.ViewFull)
	require.NoError(t, err)
	require.Equal(t, projection.ModeNone, none.Mode())

	_, err = p.Select(projection.Spec{Paths: []string{"phone"}}, projection.ViewBasic)
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed, "the mask stays within the policy")

	for _, view := range []projection.View{-1, viewCustom + 1} {
		_, err = p.Select(projection.Spec{}, view)
		require.ErrorIs(t, err, projection.ErrInvalidView, view)
		_, err = p.Select(projection.Spec{Paths: []string{"name"}}, view)
		require.ErrorIs(t, err, projection.ErrInvalidView, "an unknown view fails even under a mask")
	}
}

func TestViewFieldsAndViews(t *testing.T) {
	t.Parallel()

	p := viewPolicy(t)
	require.Equal(t, []projection.View{projection.ViewBasic, projection.ViewFull, viewCustom}, p.Views())

	basic, err := p.ViewFields(projection.ViewBasic)
	require.NoError(t, err)
	require.Equal(t, []string{"email", "name"}, basic.Paths(), "view paths are sorted and deduplicated")

	paths := basic.Paths()
	paths[0] = "mutated"
	again, err := p.ViewFields(projection.ViewBasic)
	require.NoError(t, err)
	require.Equal(t, []string{"email", "name"}, again.Paths())

	open := mustPolicy(t, projection.WithDeniedFields("secret"))
	require.Equal(t, []projection.View{projection.ViewFull}, open.Views())
	full, err := open.ViewFields(projection.ViewUnspecified)
	require.NoError(t, err)
	require.Equal(t, projection.ModeAll, full.Mode())

	_, err = open.ViewFields(projection.ViewBasic)
	require.ErrorIs(t, err, projection.ErrInvalidView, "BASIC must be configured before it is served")
}

func TestViewConfigurationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []projection.PolicyOption
		want error
	}{
		{name: "unspecified view", opts: []projection.PolicyOption{projection.WithView(projection.ViewUnspecified, "name")}, want: projection.ErrInvalidView},
		{name: "full view", opts: []projection.PolicyOption{projection.WithView(projection.ViewFull, "name")}, want: projection.ErrInvalidView},
		{name: "negative view", opts: []projection.PolicyOption{projection.WithView(-1, "name")}, want: projection.ErrInvalidView},
		{name: "empty view", opts: []projection.PolicyOption{projection.WithView(projection.ViewBasic)}, want: projection.ErrInvalidView},
		{name: "default view not configured", opts: []projection.PolicyOption{projection.WithDefaultView(projection.ViewBasic)}, want: projection.ErrInvalidView},
		{name: "unspecified default view", opts: []projection.PolicyOption{projection.WithDefaultView(projection.ViewUnspecified)}, want: projection.ErrInvalidView},
		{name: "malformed view path", opts: []projection.PolicyOption{projection.WithView(projection.ViewBasic, "a..b")}, want: projection.ErrInvalidFieldPath},
		{name: "index view path", opts: []projection.PolicyOption{projection.WithView(projection.ViewBasic, "items.0")}, want: projection.ErrUnsupportedPath},
		{
			name: "view field outside the allow-list",
			opts: []projection.PolicyOption{projection.WithAllowedFields("name"), projection.WithView(projection.ViewBasic, "email")},
			want: projection.ErrConflictingPolicy,
		},
		{
			name: "view field denied",
			opts: []projection.PolicyOption{projection.WithDeniedFields("secret"), projection.WithView(projection.ViewBasic, "secret.hash")},
			want: projection.ErrFieldNotAllowed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := projection.NewPolicy(tc.opts...)
			require.ErrorIs(t, err, tc.want)
		})
	}

	p := mustPolicy(t, projection.WithView(projection.ViewBasic, "a"), projection.WithView(projection.ViewBasic, "b"))
	f, err := p.ViewFields(projection.ViewBasic)
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, f.Paths(), "a later WithView replaces the view")
}

// TestIndexPathIsInvalidArgument pins AIP-161: a read mask cannot index a
// repeated field, whether parsed or hand-built.
func TestIndexPathIsInvalidArgument(t *testing.T) {
	t.Parallel()

	parser, err := projection.NewParser()
	require.NoError(t, err)

	for _, expr := range []string{"items.0", "items.0.name", "labels.`k`", "items.*.name", "a,*"} {
		_, err = parser.Parse(t.Context(), expr)
		require.ErrorIs(t, err, projection.ErrUnsupportedPath, expr)
	}

	p := mustPolicy(t)
	_, err = p.Validate(projection.Spec{Paths: []string{"items.0"}})
	require.ErrorIs(t, err, projection.ErrUnsupportedPath)
	_, err = p.Select(projection.Spec{Paths: []string{"items.0"}}, projection.ViewFull)
	require.ErrorIs(t, err, projection.ErrUnsupportedPath)
}

func TestTranslatorResolveView(t *testing.T) {
	t.Parallel()

	ctx, err := projection.NewTranslatorContext(viewPolicy(t, projection.WithDefaultView(projection.ViewBasic)),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithFieldPrefixMapping(map[string]string{"address.": "addr."}),
		projection.WithRequiredFields("id"),
	)
	require.NoError(t, err)

	tests := []struct {
		name string
		spec projection.Spec
		view projection.View
		want []string
	}{
		{name: "unspecified takes the default view", want: []string{"email", "id", "name"}},
		{name: "full", view: projection.ViewFull, want: []string{"addr", "created_at", "email", "id", "name"}},
		{name: "custom", view: viewCustom, want: []string{"addr.city", "id"}},
		{name: "star", spec: projection.Spec{Mode: projection.ModeAll}, view: projection.ViewBasic, want: []string{"addr", "created_at", "email", "id", "name"}},
		{name: "mask wins", spec: projection.Spec{Paths: []string{"createTime"}}, view: projection.ViewBasic, want: []string{"created_at", "id"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sel, err := ctx.ResolveView(tc.spec, tc.view)
			require.NoError(t, err)
			require.Equal(t, tc.want, sel.Include)
		})
	}

	omitted, err := ctx.Resolve(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"addr", "created_at", "email", "id", "name"}, omitted.Include, "Resolve ignores the views")

	_, err = ctx.ResolveView(projection.Spec{}, viewCustom+1)
	require.ErrorIs(t, err, projection.ErrInvalidView)
	_, err = ctx.ViewSelection(viewCustom + 1)
	require.ErrorIs(t, err, projection.ErrInvalidView)
}

// TestStoragePathsIgnoreThePolicyValidator binds a policy whose API path
// validator refuses storage-only names: storage paths follow the storage
// grammar, so the translator is still built.
func TestStoragePathsIgnoreThePolicyValidator(t *testing.T) {
	t.Parallel()

	apiOnly := func(path string) error {
		if strings.Contains(path, "_") {
			return projection.ErrInvalidFieldPath
		}

		return nil
	}
	p := mustPolicy(t, projection.WithPathValidator(apiOnly), projection.WithAllowedFields("createTime", "name"))

	ctx, err := projection.NewTranslatorContext(p,
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithDeniedStorageFields("password_hash"),
		projection.WithRequiredFields("tenant_id"),
	)
	require.NoError(t, err)
	sel, err := ctx.Resolve(projection.Spec{Paths: []string{"createTime"}})
	require.NoError(t, err)
	require.Equal(t, []string{"created_at", "tenant_id"}, sel.Include)

	_, err = p.Validate(projection.Spec{Paths: []string{"create_time"}})
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath, "API paths still use the policy validator")

	_, err = projection.NewTranslatorContext(p, projection.WithRequiredFields("items.0"))
	require.ErrorIs(t, err, projection.ErrUnsupportedPath, "storage paths use the default grammar")

	storage := func(path string) error {
		if strings.HasPrefix(path, "$") {
			return projection.ErrInvalidFieldPath
		}

		return nil
	}
	ctx, err = projection.NewTranslatorContext(p, projection.WithStoragePathValidator(storage), projection.WithRequiredFields("items.0"))
	require.NoError(t, err, "a storage validator replaces the storage grammar")
	require.Equal(t, []string{"items.0"}, ctx.RequiredFields())

	_, err = projection.NewTranslatorContext(p, projection.WithStoragePathValidator(storage), projection.WithRequiredFields("$id"))
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)
	_, err = projection.NewTranslatorContext(p, projection.WithStoragePathValidator(nil), projection.WithRequiredFields("items.0"))
	require.ErrorIs(t, err, projection.ErrUnsupportedPath, "a nil storage validator keeps the default grammar")
}

func TestViewStringAndOr(t *testing.T) {
	t.Parallel()

	require.Equal(t, "UNSPECIFIED", projection.ViewUnspecified.String())
	require.Equal(t, "BASIC", projection.ViewBasic.String())
	require.Equal(t, "FULL", projection.ViewFull.String())
	require.Equal(t, "VIEW(3)", viewCustom.String())

	require.Equal(t, projection.ViewBasic, projection.ViewUnspecified.Or(projection.ViewBasic))
	require.Equal(t, projection.ViewFull, projection.ViewFull.Or(projection.ViewBasic))
}

// TestAllFieldsSkipDeniedRoots derives every authorized field as the
// allow-list minus the denied fields: a root inside a denied field is
// dropped, at the API and at the storage level.
func TestAllFieldsSkipDeniedRoots(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, projection.WithAllowedFields("name", "hash", "alias"), projection.WithDeniedFields("hash"))
	f, err := p.Validate(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"alias", "name"}, f.Paths())

	ctx, err := projection.NewTranslatorContext(p,
		projection.WithFieldMapping(map[string]string{"alias": "pwd.v1"}),
		projection.WithDeniedStorageFields("pwd"),
	)
	require.NoError(t, err)
	sel, err := ctx.Resolve(projection.Spec{Mode: projection.ModeAll})
	require.NoError(t, err)
	require.Equal(t, []string{"name"}, sel.Include)

	_, err = projection.NewPolicy(projection.WithAllowedFields("hash"), projection.WithDeniedFields("hash"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "nothing is left to return")

	only := mustPolicy(t, projection.WithAllowedFields("alias"))
	_, err = projection.NewTranslatorContext(only, projection.WithFieldMapping(map[string]string{"alias": "pwd"}),
		projection.WithDeniedStorageFields("pwd"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "nothing is left to read")
}

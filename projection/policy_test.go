// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/projection"
)

func mustPolicy(tb testing.TB, opts ...projection.PolicyOption) *projection.Policy {
	tb.Helper()

	p, err := projection.NewPolicy(opts...)
	require.NoError(tb, err)

	return p
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t,
		projection.WithUntrustedInput(true),
		projection.WithAllowedFields("name", "address.*", "credentials.login"),
		projection.WithDeniedFields("credentials.password"),
		projection.WithView(projection.ViewBasic, "name"),
		projection.WithDefaultView(projection.ViewBasic),
	)
	require.Equal(t, []string{"credentials.password"}, p.DeniedFields())

	f, err := p.Validate(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, projection.ModeDefault, f.Mode())
	require.Equal(t, []string{"address", "credentials.login", "name"}, f.Paths(), "an omitted mask ignores the views")

	f, err = p.Validate(projection.Spec{Paths: []string{"address.city", "name"}})
	require.NoError(t, err)
	require.Equal(t, []string{"address.city", "name"}, f.Paths())

	f, err = p.Validate(projection.Spec{Mode: projection.ModeAll})
	require.NoError(t, err)
	require.Equal(t, []string{"address", "credentials.login", "name"}, f.Paths())

	f, err = p.Validate(projection.Spec{Mode: projection.ModeNone})
	require.NoError(t, err)
	require.Equal(t, projection.ModeNone, f.Mode())

	for _, paths := range [][]string{{"secret"}, {"credentials.password"}, {"credentials.password.hash"}, {"a..b"}} {
		_, err = p.Validate(projection.Spec{Paths: paths})
		require.Error(t, err, paths)
	}

	_, err = p.Validate(projection.Spec{Mode: projection.ModeAll, Paths: []string{"name"}})
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	open := mustPolicy(t, projection.WithDeniedFields("secret"))
	f, err = open.Validate(projection.Spec{Mode: projection.ModeAll})
	require.NoError(t, err)
	require.Equal(t, projection.ModeAll, f.Mode())
	require.Empty(t, f.Paths())
}

func TestNewPolicyErrors(t *testing.T) {
	t.Parallel()

	_, err := projection.NewPolicy(projection.WithUntrustedInput(true))
	require.ErrorIs(t, err, projection.ErrAllowlistRequired)

	_, err = projection.NewPolicy(projection.WithUntrustedInput(true), projection.WithAllowedFields())
	require.ErrorIs(t, err, projection.ErrAllowlistRequired)

	_, err = projection.NewPolicy(projection.WithView(projection.ViewBasic, "a..b"))
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = projection.NewPolicy(projection.WithAllowedFields("name", "credentials.*"), projection.WithDeniedFields("credentials.password"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "the allow-list root credentials carries the denied password")

	_, err = projection.NewTranslatorContext(nil)
	require.ErrorIs(t, err, projection.ErrPolicyRequired)
}

type openAccess struct{}

func (openAccess) Allows(string) bool { return true }

// TestNewPolicyUntrustedAllowlist pins the untrusted-input guard: only an allow-list that admits some but not every field
// satisfies it, as in filter and orderby. A malformed allow-list entry fails in any mode.
func TestNewPolicyUntrustedAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []projection.PolicyOption
		want error
	}{
		{name: "star allowed fields", opts: []projection.PolicyOption{projection.WithAllowedFields("*")}, want: projection.ErrAllowlistRequired},
		{name: "opaque access", opts: []projection.PolicyOption{projection.WithFieldAccess(openAccess{})}, want: projection.ErrAllowlistRequired},
		{
			name: "zero allow-list access",
			opts: []projection.PolicyOption{projection.WithFieldAccess(fieldpolicy.AllowList{})},
			want: projection.ErrAllowlistRequired,
		},
		{
			name: "star access",
			opts: []projection.PolicyOption{projection.WithFieldAccess(fieldpolicy.NewAllowList("*"))},
			want: projection.ErrAllowlistRequired,
		},
		{
			name: "two-segment stars access",
			opts: []projection.PolicyOption{projection.WithFieldAccess(fieldpolicy.NewAllowList("*.*"))},
			want: projection.ErrAllowlistRequired,
		},
		{
			name: "stars allowed fields",
			opts: []projection.PolicyOption{projection.WithAllowedFields("name", "*.*")},
			want: projection.ErrAllowlistRequired,
		},
		{
			name: "malformed allowed field",
			opts: []projection.PolicyOption{projection.WithAllowedFields("name", "a..b")},
			want: fieldpolicy.ErrMalformedPattern,
		},
		{
			name: "malformed access entry",
			opts: []projection.PolicyOption{projection.WithAllowedFields("name"), projection.WithFieldAccess(fieldpolicy.NewAllowList("**"))},
			want: fieldpolicy.ErrMalformedPattern,
		},
		{name: "allowed fields", opts: []projection.PolicyOption{projection.WithAllowedFields("name", "address.*")}},
		{
			name: "opaque access with allowed fields",
			opts: []projection.PolicyOption{projection.WithAllowedFields("name"), projection.WithFieldAccess(openAccess{})},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := projection.NewPolicy(append([]projection.PolicyOption{projection.WithUntrustedInput(true)}, tc.opts...)...)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}

	_, err := projection.NewPolicy(projection.WithAllowedFields("x."))
	require.ErrorIs(t, err, fieldpolicy.ErrMalformedPattern)
}

func TestPolicyDeclaresField(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, projection.WithUntrustedInput(true), projection.WithAllowedFields("name", "address.city", "labels.*"))
	require.True(t, p.DeclaresField("address.city"))
	require.False(t, p.DeclaresField("labels.team"), "admitted only by a wildcard")

	ctx, err := projection.NewTranslatorContext(p,
		projection.WithFieldMapping(map[string]string{"labels.team": "t.team"}),
		projection.WithFieldPrefixMapping(map[string]string{"labels": "l"}))
	require.NoError(t, err)
	require.True(t, ctx.DeclaresField("labels.team"), "an exact mapping entry declares the path")
	require.False(t, ctx.DeclaresField("labels.env"), "a prefix rule does not")
}

// TestTranslatorViewsAgainstStorageDenials reports, at construction, a view
// that reaches a denied storage path, with the sentinel of where the fields
// come from.
func TestTranslatorViewsAgainstStorageDenials(t *testing.T) {
	t.Parallel()

	explicit := mustPolicy(t, projection.WithView(projection.ViewBasic, "name"))
	_, err := projection.NewTranslatorContext(explicit, projection.WithDeniedStorageFields("name"))
	require.ErrorIs(t, err, projection.ErrConflictingPolicy)

	derived := mustPolicy(t, projection.WithAllowedFields("name"))
	_, err = projection.NewTranslatorContext(derived, projection.WithDeniedStorageFields("name"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)

	_, err = projection.NewTranslatorContext(derived, projection.WithRequiredFields("items.0"))
	require.ErrorIs(t, err, projection.ErrUnsupportedPath)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/projection"
)

func TestNewTranslatorContextErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []any
		wantErr error
	}{
		{
			name:    "untrusted without allow-list",
			opts:    []any{projection.WithUntrustedInput(true)},
			wantErr: projection.ErrAllowlistRequired,
		},
		{
			name:    "untrusted with empty allow-list",
			opts:    []any{projection.WithUntrustedInput(true), projection.WithAllowedFields()},
			wantErr: projection.ErrAllowlistRequired,
		},
		{
			name:    "empty allow-list enumerates no field",
			opts:    []any{projection.WithAllowedFields()},
			wantErr: projection.ErrAllFieldsUnresolvable,
		},
		{
			name:    "malformed denied path",
			opts:    []any{projection.WithDeniedFields("a..b")},
			wantErr: projection.ErrInvalidFieldPath,
		},
		{
			name:    "unsupported required path",
			opts:    []any{projection.WithRequiredFields("items.0")},
			wantErr: projection.ErrUnsupportedPath,
		},
		{
			name: "required overlaps denied storage",
			opts: []any{
				projection.WithRequiredFields("secret.hash"),
				projection.WithDeniedStorageFields("secret"),
			},
			wantErr: projection.ErrConflictingPolicy,
		},
		{
			name: "required overlaps mapped denied field",
			opts: []any{
				projection.WithFieldMapping(map[string]string{"password": "pwd"}),
				projection.WithDeniedFields("password"),
				projection.WithRequiredFields("pwd"),
			},
			wantErr: projection.ErrConflictingPolicy,
		},
		{
			name: "view field outside allow-list",
			opts: []any{
				projection.WithAllowedFields("name"),
				projection.WithView(projection.ViewBasic, "email"),
			},
			wantErr: projection.ErrConflictingPolicy,
		},
		{
			name: "view field denied",
			opts: []any{
				projection.WithView(projection.ViewBasic, "credentials"),
				projection.WithDeniedFields("credentials.password"),
			},
			wantErr: projection.ErrConflictingPolicy,
		},
		{
			name: "view field reaches denied storage",
			opts: []any{
				projection.WithView(projection.ViewBasic, "name"),
				projection.WithDeniedFields("secret"),
				projection.WithFieldMapping(map[string]string{"name": "secret"}),
			},
			wantErr: projection.ErrConflictingPolicy,
		},
		{
			name: "allow-list root reaches denied storage",
			opts: []any{
				projection.WithAllowedFields("name"),
				projection.WithDeniedStorageFields("name"),
			},
			wantErr: projection.ErrAllFieldsUnresolvable,
		},
		{
			name: "allow-list root carries a denied field",
			opts: []any{
				projection.WithAllowedFields("name", "credentials.*"),
				projection.WithDeniedFields("credentials.password"),
			},
			wantErr: projection.ErrAllFieldsUnresolvable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := newContext(tc.opts...)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(
		projection.WithUntrustedInput(true),
		projection.WithAllowedFields("name", "email", "address.*", "credentials.login", "createTime", "alias"),
		projection.WithDeniedFields("credentials.password"),
		projection.WithDeniedStorageFields("secret_col"),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at", "alias": "secret_col"}),
		projection.WithFieldPrefixMapping(map[string]string{"address.": "addr."}),
		projection.WithRequiredFields("_id"),
		projection.WithView(projection.ViewBasic, "name", "email"),
	)
	require.NoError(t, err)

	tests := []struct {
		name    string
		paths   []string
		want    []string
		wantErr error
	}{
		{name: "omitted mask selects every authorized field", want: []string{"_id", "addr", "created_at", "credentials.login", "email", "name"}}, // alias reaches denied storage
		{name: "mapped and required", paths: []string{"createTime", "name"}, want: []string{"_id", "created_at", "name"}},
		{name: "prefix mapping", paths: []string{"address.city"}, want: []string{"_id", "addr.city"}},
		{name: "allowed sibling of denied", paths: []string{"credentials.login"}, want: []string{"_id", "credentials.login"}},
		{name: "not allowed", paths: []string{"phone"}, wantErr: projection.ErrFieldNotAllowed},
		{name: "bare parent of wildcard", paths: []string{"address"}, wantErr: projection.ErrFieldNotAllowed},
		{name: "denied exact", paths: []string{"credentials.password"}, wantErr: projection.ErrFieldNotAllowed},
		{name: "denied descendant", paths: []string{"credentials.password.hash"}, wantErr: projection.ErrFieldNotAllowed},
		{name: "alias onto denied storage", paths: []string{"alias"}, wantErr: projection.ErrFieldNotAllowed},
		{name: "hand-built operator path", paths: []string{"$where"}, wantErr: projection.ErrInvalidFieldPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sel, err := ctx.Resolve(projection.Spec{Paths: tc.paths})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, sel.Include)
			require.Empty(t, sel.Exclude)
		})
	}
}

func TestResolveDeniedAncestor(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(projection.WithDeniedFields("credentials.password"))
	require.NoError(t, err)

	_, err = ctx.Resolve(projection.Spec{Paths: []string{"credentials"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed, "a parent would carry the denied child along")
}

func TestFullViewSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opts        []any
		wantInclude []string
		wantExclude []string
	}{
		{name: "no policy selects everything"},
		{name: "lone star selects everything", opts: []any{projection.WithAllowedFields("*")}},
		{
			name:        "denied fields become an exclusion",
			opts:        []any{projection.WithDeniedFields("password"), projection.WithDeniedStorageFields("token")},
			wantExclude: []string{"password", "token"},
		},
		{
			name:        "allow-list roots",
			opts:        []any{projection.WithAllowedFields("name", "address.*", "address.city")},
			wantInclude: []string{"address", "address.city", "name"},
		},
		{
			name: "allow-list roots are mapped and joined by required fields",
			opts: []any{
				projection.WithAllowedFields("createTime"),
				projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
				projection.WithRequiredFields("id"),
			},
			wantInclude: []string{"created_at", "id"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, err := newContext(tc.opts...)
			require.NoError(t, err)

			def, err := ctx.ViewSelection(projection.ViewFull)
			require.NoError(t, err)
			require.Equal(t, tc.wantInclude, def.Include)
			require.Equal(t, tc.wantExclude, def.Exclude)
			require.Equal(t, len(tc.wantInclude)+len(tc.wantExclude) == 0, def.IsAll())

			omitted, err := ctx.Resolve(projection.Spec{})
			require.NoError(t, err)
			require.Equal(t, def, omitted, "an omitted mask selects every authorized field")

			if len(def.Include) > 0 {
				def.Include[0] = "mutated"
				again, err := ctx.ViewSelection(projection.ViewFull)
				require.NoError(t, err)
				require.NotEqual(t, "mutated", again.Include[0], "ViewSelection returns a copy")
			}
		})
	}
}

func TestMappingCannotBypassDenial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []any
		path string
	}{
		{
			name: "alias onto a remapped descendant of a denied field",
			opts: []any{
				projection.WithDeniedFields("credentials"),
				projection.WithFieldMapping(map[string]string{"credentials.password": "pwd", "alias": "pwd"}),
			},
			path: "alias",
		},
		{
			name: "alias under a denied field's prefix target",
			opts: []any{
				projection.WithDeniedFields("credentials"),
				projection.WithFieldPrefixMapping(map[string]string{"credentials.": "cred."}),
				projection.WithFieldMapping(map[string]string{"alias": "cred.password"}),
			},
			path: "alias",
		},
		{
			name: "alias under an exact ancestor's target",
			opts: []any{
				projection.WithDeniedFields("credentials.password"),
				projection.WithFieldMapping(map[string]string{"credentials": "c", "alias": "c.password"}),
			},
			path: "alias",
		},
		{
			name: "alias under a prefix target shadowed by an exact entry",
			opts: []any{
				projection.WithDeniedFields("credentials.password"),
				projection.WithFieldPrefixMapping(map[string]string{"credentials.": "c."}),
				projection.WithFieldMapping(map[string]string{"credentials.password": "secret", "alias": "c"}),
			},
			path: "alias",
		},
		{
			name: "parent whose moved descendant is denied in storage",
			opts: []any{
				projection.WithDeniedStorageFields("pwd"),
				projection.WithFieldMapping(map[string]string{"credentials.password": "pwd"}),
			},
			path: "credentials",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, err := newContext(tc.opts...)
			require.NoError(t, err)
			_, err = ctx.Resolve(projection.Spec{Paths: []string{tc.path}})
			require.ErrorIs(t, err, projection.ErrFieldNotAllowed)
		})
	}
}

func TestDeniedRemappedDescendantExcludedByDefault(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(
		projection.WithDeniedFields("credentials"),
		projection.WithFieldMapping(map[string]string{"credentials.password": "pwd"}),
	)
	require.NoError(t, err)
	sel, err := ctx.Resolve(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"credentials", "pwd"}, sel.Exclude)
}

func TestSubtreeSelection(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(
		projection.WithAllowedFields("name", "address.*"),
		projection.WithFieldPrefixMapping(map[string]string{"address.": "addr."}),
		projection.WithFieldMapping(map[string]string{"address.city": "city_col"}),
	)
	require.NoError(t, err)

	sel, err := ctx.Resolve(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"addr", "city_col", "name"}, sel.Include,
		"a wildcard root follows its prefix rule and keeps moved descendants")

	sel, err = ctx.Resolve(projection.Spec{Paths: []string{"address.zip", "address.city"}})
	require.NoError(t, err)
	require.Equal(t, []string{"addr.zip", "city_col"}, sel.Include)
}

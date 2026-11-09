// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

// projectionDialectCase is one built-in dialect with its quoting and identifier limit.
type projectionDialectCase struct {
	name     string
	dialect  sqlbase.ProjectionDialect
	quote    string
	maxIdent int
	// maxParts is the largest number of dot-separated parts; zero means any.
	maxParts int
}

func projectionDialects() []projectionDialectCase {
	return []projectionDialectCase{
		{name: "postgres", dialect: postgres.ProjectionDialect(), quote: `"`, maxIdent: 63, maxParts: 2},
		{name: "mariadb", dialect: mariadb.ProjectionDialect(), quote: "`", maxIdent: 64, maxParts: 2},
		{name: "clickhouse", dialect: clickhouse.ProjectionDialect(), quote: "`", maxIdent: 255},
	}
}

// q quotes each comma-separated column, part by part, the way the dialect does.
func (d projectionDialectCase) q(columns ...string) string {
	out := make([]string, len(columns))
	for i, c := range columns {
		parts := strings.Split(c, ".")
		for j, p := range parts {
			parts[j] = d.quote + p + d.quote
		}

		out[i] = strings.Join(parts, ".")
	}

	return strings.Join(out, ", ")
}

func TestTranslator(t *testing.T) {
	t.Parallel()

	for _, d := range projectionDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			mapping := projection.WithFieldMapping(map[string]string{
				"createTime": "created_at",
				"author":     "authors.name",
				"deep":       "a.b.c",
				"boundary":   strings.Repeat("c", d.maxIdent),
				"long":       strings.Repeat("c", d.maxIdent+1),
				"raw":        "lower(name)",
			})

			allowed := []string{"name", "email", "createTime", "author", "boundary"}
			if d.maxParts == 0 {
				allowed = append(allowed, "deep")
			}

			tr, err := newTranslator(d.dialect,
				projection.WithUntrustedInput(true),
				projection.WithAllowedFields(allowed...),
				mapping,
				projection.WithRequiredFields("id"),
				projection.WithView(projection.ViewBasic, "name"),
			)
			require.NoError(t, err)

			// Every allowed field is part of FULL, so a field that is not a
			// column fails at construction.
			bad := []string{"long", "raw"}
			if d.maxParts == 2 {
				bad = append(bad, "deep")
			}

			for _, field := range bad {
				_, err = newTranslator(d.dialect, projection.WithAllowedFields("name", field), mapping)
				require.ErrorIs(t, err, projection.ErrInvalidFieldPath, field)
			}

			// With no allow-list FULL renders *, so the same fields fail per request.
			open, err := newTranslator(d.dialect, mapping)
			require.NoError(t, err)

			for _, field := range bad {
				_, err = open.Translate(projection.Spec{Paths: []string{field}})
				require.ErrorIs(t, err, projection.ErrInvalidFieldPath, field)
			}

			all := d.q("authors.name", strings.Repeat("c", d.maxIdent), "created_at", "email", "id", "name")
			if d.maxParts == 0 {
				all = d.q("a.b.c", "authors.name", strings.Repeat("c", d.maxIdent), "created_at", "email", "id", "name")
			}

			for _, tc := range []struct {
				name  string
				paths []string
				want  string
				err   error
			}{
				{name: "mapped and required", paths: []string{"name", "createTime"}, want: d.q("created_at", "id", "name")},
				{name: "omitted mask selects every allowed column", want: all},
				{name: "qualified", paths: []string{"author"}, want: d.q("authors.name", "id")},
				{name: "identifier boundary", paths: []string{"boundary"}, want: d.q(strings.Repeat("c", d.maxIdent), "id")},
				{name: "not allowed", paths: []string{"password"}, err: projection.ErrFieldNotAllowed},
				{name: "index", paths: []string{"name.0"}, err: projection.ErrUnsupportedPath},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					got, err := tr.Translate(projection.Spec{Paths: tc.paths})
					if tc.want == "" {
						require.ErrorIs(t, err, tc.err)
						require.Empty(t, got)

						return
					}

					require.NoError(t, err)
					require.Equal(t, tc.want, got)
				})
			}

			for _, tc := range []struct {
				name string
				spec projection.Spec
				view projection.View
				want string
			}{
				{name: "unspecified takes FULL", want: all},
				{name: "basic", view: projection.ViewBasic, want: d.q("id", "name")},
				{name: "star wins over basic", spec: projection.Spec{Mode: projection.ModeAll}, view: projection.ViewBasic, want: all},
				{name: "mask wins over basic", spec: projection.Spec{Paths: []string{"email"}}, view: projection.ViewBasic, want: d.q("email", "id")},
			} {
				t.Run("view/"+tc.name, func(t *testing.T) {
					t.Parallel()

					got, err := tr.TranslateView(tc.spec, tc.view)
					require.NoError(t, err)
					require.Equal(t, tc.want, got)
				})
			}

			_, err = tr.TranslateView(projection.Spec{}, projection.ViewFull+1)
			require.ErrorIs(t, err, projection.ErrInvalidView)
		})
	}
}

func TestTranslatorAllFields(t *testing.T) {
	t.Parallel()

	for _, d := range projectionDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			tr, err := newTranslator(d.dialect,
				projection.WithAllowedFields("name", "email", "password"),
				projection.WithDeniedFields("password"),
				projection.WithView(projection.ViewBasic, "name"),
				projection.WithDefaultView(projection.ViewBasic),
				projection.WithRequiredFields("id"),
			)
			require.NoError(t, err)
			got, err := tr.Translate(projection.Spec{})
			require.NoError(t, err)
			require.Equal(t, d.q("email", "id", "name"), got, "an omitted mask returns every authorized column, not the default view")

			got, err = tr.TranslateView(projection.Spec{}, projection.ViewUnspecified)
			require.NoError(t, err)
			require.Equal(t, d.q("id", "name"), got, "an unspecified view takes the default view")

			if d.name == "clickhouse" {
				t.Log("ClickHouse renders exclusions: see TestClickHouseExcept")
			} else {
				_, err = newTranslator(d.dialect, projection.WithDeniedFields("password"))
				require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)

				_, err = newTranslator(d.dialect, projection.WithDeniedFields("password"), projection.WithView(projection.ViewBasic, "name"))
				require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "a view does not replace FULL")
			}

			_, err = newTranslator(d.dialect, projection.WithUntrustedInput(true))
			require.ErrorIs(t, err, projection.ErrAllowlistRequired)
		})
	}

	_, err := newTranslator(sqlbase.ProjectionDialect{})
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = sqlbase.NewProjectionTranslator(nil, postgres.ProjectionDialect())
	require.ErrorIs(t, err, projection.ErrPolicyRequired)
}

func TestClickHouseExcept(t *testing.T) {
	t.Parallel()

	tr, err := newTranslator(clickhouse.ProjectionDialect(),
		projection.WithDeniedFields("password", "secret"),
		projection.WithView(projection.ViewBasic, "name"),
		projection.WithFieldMapping(map[string]string{"secret": "api_key"}),
	)
	require.NoError(t, err)

	for _, spec := range []projection.Spec{{}, {Mode: projection.ModeAll}} {
		got, err := tr.Translate(spec)
		require.NoError(t, err)
		require.Equal(t, "* EXCEPT (`api_key`, `password`)", got, "every authorized column excludes the denied ones in storage names")
	}

	got, err := tr.TranslateView(projection.Spec{}, projection.ViewFull)
	require.NoError(t, err)
	require.Equal(t, "* EXCEPT (`api_key`, `password`)", got)

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewBasic)
	require.NoError(t, err)
	require.Equal(t, "`name`", got)

	got, err = tr.Translate(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)
	require.Equal(t, "`name`", got)

	_, err = tr.Translate(projection.Spec{Paths: []string{"password"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed)

	_, err = newTranslator(clickhouse.ProjectionDialect(), projection.WithDeniedFields("profile.ssn"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "* EXCEPT cannot exclude a subcolumn; it would leak through its parent")
	require.ErrorIs(t, err, projection.ErrUnsupportedSelection)

	_, err = newTranslator(clickhouse.ProjectionDialect(), projection.WithDeniedStorageFields("raw-col"))
	require.Error(t, err, "a denied storage name outside the grammar fails at construction")

	ctx, err := sqlbase.NewProjectionContext(mustPolicy(t, projection.WithAllowedFields("name")), clickhouse.ProjectionDialect())
	require.NoError(t, err)

	_, err = sqlbase.NewProjectionContext(mustPolicy(t, projection.WithDeniedFields("password")), upper{})
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "an application resolver has no exclusion")

	_, err = sqlbase.TranslateProjection(ctx, upper{}, projection.Spec{Mode: projection.ModeNone})
	require.ErrorIs(t, err, projection.ErrUnsupportedSelection)
}

// upper is an application resolver that cannot render exclusions.
type upper struct{}

func (upper) Table(name string) (string, error) { return strings.ToUpper(name), nil }

func mustPolicy(t *testing.T, opts ...projection.PolicyOption) *projection.Policy {
	t.Helper()

	p, err := projection.NewPolicy(opts...)
	require.NoError(t, err)

	return p
}

// TestProjectionTranslator_DeclaredQualifiers pins the declared-field rule of PostgreSQL, MariaDB and ClickHouse for read masks: a
// dotted path must be an exact allow-list or mapping entry, or the request fails with ErrFieldNotAllowed, also through a view
// method. Views and every authorized field are configured by the service.
func TestProjectionTranslator_DeclaredQualifiers(t *testing.T) {
	t.Parallel()

	for _, d := range projectionDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			tr, err := newTranslator(d.dialect,
				projection.WithUntrustedInput(true),
				projection.WithAllowedFields("name", "address.city", "t.*"),
				projection.WithFieldMapping(map[string]string{"t.team": "team"}),
				projection.WithView(projection.ViewBasic, "address.city"),
			)
			require.NoError(t, err)

			got, err := tr.Translate(projection.Spec{Paths: []string{"address.city", "t.team"}})
			require.NoError(t, err)
			require.Equal(t, d.q("address.city", "team"), got)

			got, err = tr.TranslateView(projection.Spec{}, projection.ViewBasic)
			require.NoError(t, err)
			require.Equal(t, d.q("address.city"), got)

			for _, translate := range []func(projection.Spec) (string, error){
				tr.Translate,
				func(s projection.Spec) (string, error) { return tr.TranslateView(s, projection.ViewBasic) },
			} {
				got, err = translate(projection.Spec{Paths: []string{"name", "t.secret"}})
				require.ErrorIs(t, err, projection.ErrFieldNotAllowed)
				require.Empty(t, got)
			}
		})
	}
}

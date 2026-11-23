// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
)

func TestCaseInsensitiveContainsRendering(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"text": sqlbase.PresenceScalar("title", sqlbase.Text), "tags": sqlbase.Array("tags", sqlbase.Text),
		"needle": sqlbase.PresenceScalar("query", sqlbase.Text),
	})
	require.NoError(t, err)

	names := []string{"tags", "text", "tags"}
	option := postgres.WithSchemaCaseInsensitiveContainsFields(names...)
	names[0] = "bad"
	s, err := postgres.NewSchema(fields, option)
	require.NoError(t, err)

	for _, tc := range []struct {
		expr, fragment string
		args           []any
		want           bool
	}{
		{`text.contains("иВе")`, `strpos(lower(("title") COLLATE pg_catalog.pg_c_utf8), lower((($1)::text) COLLATE pg_catalog.pg_c_utf8))`, []any{"иВе"}, true},
		{`!tags.contains(needle)`, `strpos(lower(("_aip_element_0") COLLATE pg_catalog.pg_c_utf8), lower(("query") COLLATE pg_catalog.pg_c_utf8))`, nil, false},
		{`needle.contains(text)`, `strpos("query", "title")`, nil, false},
		{`text.startsWith("при")`, `starts_with("title", ($1)::text)`, []any{"при"}, false},
		{`text == "привет"`, `"title" = ($1)::text`, []any{"привет"}, false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			p := compile(t, s, tc.expr)
			query, args, err := p.SQLContract()
			require.NoError(t, err)
			require.Contains(t, query, tc.fragment)
			require.Equal(t, tc.args, args)

			got, err := p.Evaluate(map[string]any{"title": "ПрИвЕт", "query": "иВе", "tags": []any{nil, "мир", "ПРИВЕТИК"}}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	p := compile(t, s, `!tags.contains(needle)`)
	ok, err := p.Evaluate(map[string]any{"tags": []string{}}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, ok)
	p = compile(t, s, `text.contains("ё")`)
	_, err = p.Evaluate(map[string]any{"title": strings.Repeat("Ё", 50)}, 105, nil)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)

	for range 8 {
		t.Run("shared program", func(t *testing.T) {
			t.Parallel()

			ok, err := p.Evaluate(map[string]any{"title": "ЁЖИК"}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.True(t, ok)
		})
	}
}

func TestCaseInsensitiveContainsRequiresPlainText(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"text": sqlbase.Scalar("title", sqlbase.Text), "uuid": sqlbase.Scalar("uuid", sqlbase.UUID),
		"path": sqlbase.JSONPath("object", sqlbase.Text, "path"), "ids": sqlbase.Array("ids", sqlbase.Integer),
	})
	require.NoError(t, err)

	for _, name := range []string{"title", " text ", "uuid", "path", "ids"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := postgres.NewSchema(fields, postgres.WithSchemaCaseInsensitiveContainsFields(name))
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}

	s, err := postgres.NewSchema(fields, postgres.WithSchemaCaseInsensitiveContainsFields("text"))
	require.NoError(t, err)
	_, err = s.Compile(filtertest.MustPolicy(t, filter.WithAllowedFields("uuid")), filtertest.MustParseFilter(t, `text.contains("a")`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestCaseInsensitiveContainsPreflight(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"},
		listing.NewProfile(listing.WithCaseInsensitiveContainsFields("id")))

	for _, supported := range []bool{false, true} {
		name := "unsupported"
		if supported {
			name = "certified"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
				if strings.Contains(query, "server_version_num") {
					require.Contains(t, query, "server_encoding")
					require.Contains(t, query, "to_regcollation('pg_catalog.pg_c_utf8')")

					return sqlbasetest.Reply{Columns: []string{"supported"}, Rows: [][]driver.Value{{supported}}}
				}

				return sqlbasetest.Reply{Columns: []string{"name", "type", "udt", "collation", "nullable", "precision"},
					Rows: [][]driver.Value{{"id", "text", "text", "C", "NO", nil}}}
			})
			engine, err := postgres.NewListEngine(db, nil, schema, "terms", []string{"id"}, func(id *string) []any { return []any{id} })
			require.NoError(t, err)
			require.Empty(t, fake.Calls())

			err = engine.CheckSchema(t.Context(), db)
			if supported {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, listing.ErrInvalidSchema)
			}
		})
	}
}

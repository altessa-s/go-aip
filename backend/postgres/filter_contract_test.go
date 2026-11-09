// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestContractRendering(t *testing.T) {
	t.Parallel()

	schemaFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name":  sqlbase.Scalar("name", sqlbase.Text),
		"note":  sqlbase.Scalar("note", sqlbase.Text),
		"tags":  sqlbase.Array("tags", sqlbase.Text),
		"count": sqlbase.Scalar("count", sqlbase.Integer),
	})
	require.NoError(t, err)
	schema, err := NewSchema(schemaFields, WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	for _, tc := range []struct {
		expr, contains string
		args           []any
	}{
		{`name == "n"`, `"name" = ($1)::text`, []any{"n"}},
		{`name != "n"`, `"name" <> ($1)::text`, []any{"n"}},
		{`name < "n"`, `"name" < ($1)::text`, []any{"n"}},
		{`name <= "n"`, `"name" <= ($1)::text`, []any{"n"}},
		{`name > "n"`, `"name" > ($1)::text`, []any{"n"}},
		{`name >= "n"`, `"name" >= ($1)::text`, []any{"n"}},
		{`name in ["n", "m"]`, `"name" = ANY(ARRAY`, []any{"n", "m"}},
		{`note == null`, `"note" = ($1)::text`, []any{""}},
		{`note != null`, `"note" <> ($1)::text`, []any{""}},
		{`size(name) > 2`, `char_length("name")`, []any{int64(2)}},
		{`size(tags) == 0`, `cardinality("tags")`, []any{int64(0)}},
		{`"x" in tags`, `($1)::text = ANY("tags")`, []any{"x"}},
		{`tags == "x"`, `($1)::text = ANY("tags")`, []any{"x"}},
		{`tags < "x"`, `($1)::text > ANY("tags")`, []any{"x"}},
		{`tags != "x"`, `(NOT COALESCE((($1)::text = ANY("tags")), FALSE))`, []any{"x"}},
		{`tags.startsWith("ab")`, `EXISTS (SELECT 1 FROM unnest("tags")`, []any{"ab"}},
		{`tags.contains("ab")`, `strpos("_aip_element_0", ($1)::text) > 0`, []any{"ab"}},
		{`tags.endsWith("ab")`, `right("_aip_element_0", length(($1)::text))`, []any{"ab"}},
		{`size(tags) >= 1`, `cardinality("tags")`, []any{int64(1)}},
		{`name.matches("(?i)abc")`, `"name" ~* ($1)::text`, []any{"(?p)abc"}},
		{`name.matches("^a.*b$")`, `"name" ~ ($1)::text`, []any{"(?p)^a.*b$"}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			sql, args, translateErr := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, translateErr)
			require.Contains(t, sql, tc.contains)
			require.NotContains(t, sql, `COLLATE "C"`)
			require.Equal(t, tc.args, args)
		})
	}

	t.Run("has scalar", func(t *testing.T) {
		t.Parallel()

		node := &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "note"}}
		sql, args, err := tr.Translate(node)
		require.NoError(t, err)
		require.Contains(t, sql, `"note" <> ($1)::text`)
		require.Equal(t, []any{""}, args)
	})
}

func TestPresenceContractRendering(t *testing.T) {
	t.Parallel()

	schemaFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"rating":       sqlbase.PresenceScalar("rating", sqlbase.Integer),
		"score":        sqlbase.PresenceScalar("score", sqlbase.Text),
		"rating.value": sqlbase.PresenceScalar("rating", sqlbase.Integer),
	})
	require.NoError(t, err)
	schema, err := NewSchema(schemaFields, WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"rating": filter.FieldKindInt, "score": filter.FieldKindString, "rating.value": filter.FieldKindInt})), schema)
	require.NoError(t, err)

	cases := []struct {
		expr, want string
		args       []any
	}{
		{`rating != 1`, `("rating" <> ($1)::bigint OR "rating" IS NULL)`, []any{int64(1)}},
		{`!(rating == 1)`, `("rating" <> ($1)::bigint OR "rating" IS NULL)`, []any{int64(1)}},
		{`!(rating != 1)`, `("rating" = ($1)::bigint)`, []any{int64(1)}},
		{`!(rating in [1, 2])`, `(NOT ("rating" = ANY(ARRAY[($1)::bigint, ($2)::bigint]::bigint[])) OR ("rating") IS NULL)`, []any{int64(1), int64(2)}},
		{`!score.startsWith("a")`, `(NOT starts_with("score", ($1)::text) OR ("score") IS NULL)`, []any{"a"}},
		{`!(rating == 1 || rating > 3)`, `((("rating" <> ($1)::bigint OR "rating" IS NULL)) AND ((NOT ("rating" > ($2)::bigint))))`, []any{int64(1), int64(3)}},
		{`rating == null`, `("rating") IS NULL`, nil},
		{`has(rating.value)`, `("rating") IS NOT NULL`, nil},
		{`size(score) == 0`, `COALESCE(char_length("score"), 0) = ($1)::bigint`, []any{int64(0)}},
		{`!(rating > 1)`, `(NOT ("rating" > ($1)::bigint))`, []any{int64(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Contains(t, sql, tc.want)
			require.Equal(t, tc.args, args)
		})
	}
}

func TestTranslatorContractOptionEnablesContractCompilation(t *testing.T) {
	t.Parallel()

	schemaFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"tags": sqlbase.Array("tags", sqlbase.Text), "name": sqlbase.Scalar("name", sqlbase.Text),
	})
	require.NoError(t, err)
	schema, err := NewSchema(schemaFields, WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)
	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `tags in ["go"]`))
	require.NoError(t, err)
	require.Contains(t, sql, `"tags" && ARRAY`)
	require.Equal(t, []any{"go"}, args)

	sql, _, err = tr.Translate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "name"}})
	require.NoError(t, err)
	require.Contains(t, sql, `"name" <>`)
}

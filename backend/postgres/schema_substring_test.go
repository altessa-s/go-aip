// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// substringSchema declares a NOT NULL text column, a presence text column and a text JSON path.
func substringSchema(t *testing.T, opts ...postgres.SchemaOption) *postgres.Schema {
	t.Helper()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text), "score": sqlbase.PresenceScalar("score", sqlbase.Text),
		"doc.city": sqlbase.JSONPath("doc", sqlbase.Text, "city"),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(fields, opts...)
	require.NoError(t, err)

	return s
}

// TestSubstringEvaluate pins Program.Evaluate on substring(): code points, the open end, the length guard and the NULL
// text, which are unknown and match neither a comparison nor its negation.
func TestSubstringEvaluate(t *testing.T) {
	t.Parallel()

	schema := substringSchema(t)
	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`name.substring(0, 2) == "жё"`, map[string]any{"name": "жёлтый"}, true},
		{`name.substring(2) == "лтый"`, map[string]any{"name": "жёлтый"}, true},
		{`name.substring(6) == ""`, map[string]any{"name": "жёлтый"}, true},
		{`name.substring(7) == ""`, map[string]any{"name": "жёлтый"}, false},
		{`!(name.substring(7) == "")`, map[string]any{"name": "жёлтый"}, false},
		{`name.substring(1, 7) != "x"`, map[string]any{"name": "жёлтый"}, false},
		{`name.substring(1, 6) != "x"`, map[string]any{"name": "жёлтый"}, true},
		{`name.substring(0) != "x"`, map[string]any{"name": nil}, false},
		{`!(name.substring(0) == "x")`, map[string]any{"name": nil}, false},
		{`name.substring(0) != null`, map[string]any{"name": nil}, false},
		{`name.substring(0) != null`, map[string]any{"name": ""}, true},
		{`doc.city.substring(0, 1) == "R"`, map[string]any{"doc": `{"city": "Rome"}`}, true},
		{`doc.city.substring(0, 1) != "R"`, map[string]any{"doc": `{}`}, false},
		{`name.substring(0, 2147483648) == ""`, map[string]any{"name": "x"}, false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			got, err := compile(t, schema, tc.expr).Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestSubstringEvaluateContract pins substring() under the listing contract: a NULL presence column is unknown, a NULL read
// from a NOT NULL column stands for the empty string.
func TestSubstringEvaluateContract(t *testing.T) {
	t.Parallel()

	schema := substringSchema(t, postgres.WithSchemaContractMode(true))
	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))

	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`score.substring(0) != "x"`, map[string]any{"name": "a", "score": nil}, false},
		{`!(score.substring(0) == "x")`, map[string]any{"name": "a", "score": nil}, false},
		{`score.substring(1) == "b"`, map[string]any{"name": "a", "score": "ab"}, true},
		{`name.substring(0) == ""`, map[string]any{"name": nil}, true},
		{`name.substring(1) != "x"`, map[string]any{"name": nil}, false},
		{`!(name.substring(0, 1) in ["a"])`, map[string]any{"name": "b"}, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			p, err := schema.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			got, err := p.Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestSubstringEvaluateLimits charges the text a substring() reads to the evaluation budget.
func TestSubstringEvaluateLimits(t *testing.T) {
	t.Parallel()

	p := compile(t, substringSchema(t), `name.substring(1) == "bcdefgh"`)
	_, err := p.Evaluate(map[string]any{"name": "abcdefgh"}, 4, nil)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)

	_, err = p.Evaluate(map[string]any{"name": int64(1)}, postgres.DefaultMaxDataBudget, nil)
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}

// TestSubstringTranslator renders substring() through the schema translator, parameterized and in contract mode.
func TestSubstringTranslator(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	node := filtertest.MustParseFilter(t, `!(score.substring(1, 3) == "ab")`)

	tr, err := postgres.NewFilterTranslatorWithSchema(policy, substringSchema(t))
	require.NoError(t, err)
	sql, args, err := tr.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `(((CASE WHEN char_length("score") >= 3 THEN substr("score", 2, 2) END) COLLATE "C") <> (($1)::text COLLATE "C"))`, sql)
	require.Equal(t, []any{"ab"}, args)

	contract, err := postgres.NewFilterTranslatorWithSchema(policy, substringSchema(t, postgres.WithSchemaContractMode(true)))
	require.NoError(t, err)
	sql, args, err = contract.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `((CASE WHEN char_length("score") >= 3 THEN substr("score", 2, 2) END) <> ($1)::text)`, sql)
	require.Equal(t, []any{"ab"}, args)

	inline, err := contract.TranslateInline(node)
	require.NoError(t, err)
	require.Equal(t, `((CASE WHEN char_length("score") >= 3 THEN substr("score", 2, 2) END) <> (E'ab')::text)`, inline)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// jsonSchema is a JSONB column, a JSONB path and typed text paths over the same document, ordered with a byte-wise string
// comparator.
func jsonSchema(t *testing.T, opts ...postgres.SchemaOption) *postgres.Schema {
	t.Helper()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"json":  sqlbase.Scalar("doc", sqlbase.JSON),
		"doc.o": sqlbase.JSONPath("doc", sqlbase.Text, "o"),
		"doc.l": sqlbase.JSONPath("doc", sqlbase.Text, "l"),
	})
	require.NoError(t, err)

	s, err := postgres.NewSchema(fields, append([]postgres.SchemaOption{postgres.WithSchemaJSONBStringCompare(strings.Compare)}, opts...)...)
	require.NoError(t, err)

	return s
}

// TestJSONBOrdering pins the JSONB ordering PostgreSQL applies to arrays and objects: an object sorts above an array, a longer
// array or an object with more pairs sorts higher, equal-length values compare element by element (objects key by key), and an
// empty top-level array sorts below every other value.
func TestJSONBOrdering(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	s := jsonSchema(t)

	for _, tc := range []struct {
		doc, expr string
		want      bool
	}{
		{`[1, 2]`, `json < '[1, 2, 3]'`, true},
		{`[1, 2, 3]`, `json < '[1, 2]'`, false},
		{`[1, "b"]`, `json > '[1, "a"]'`, true},
		{`[1, "a"]`, `json > '[1, "a"]'`, false},
		{`[1, [2, 3]]`, `json >= '[1, [2, 3]]'`, true},
		{`[1]`, `json < '{}'`, true},
		{`{"a": 1, "b": 2}`, `json > '{"a": 1}'`, true},
		{`{"a": 1, "b": [true, null]}`, `json >= '{"b": [true, null], "a": 1}'`, true},
		{`{"a": 1, "b": [true, null]}`, `json <= '{"b": [true, null], "a": 1}'`, true},
		{`{"a": 1, "b": 2}`, `json < '{"a": 1, "c": 2}'`, true},
		{`{"a": 1, "b": 2}`, `json < '{"a": 1, "b": 3}'`, true},
		{`[]`, `json < '"x"'`, true},
		{`"x"`, `json > '[]'`, true},
	} {
		t.Run(tc.doc+" "+tc.expr, func(t *testing.T) {
			t.Parallel()

			p, err := s.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)

			got, err := p.Evaluate(map[string]any{"doc": tc.doc}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestJSONTextOfContainers pins the text a typed text path reads from a JSON container, as PostgreSQL's #>> spells it: keys
// in storage order (shorter first), ", " and ": " separators, and only quotes, backslashes and control characters escaped.
func TestJSONTextOfContainers(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	s := jsonSchema(t)
	doc := `{"o": {"bb": 1, "a": [1.50, "x"], "c": null}, "l": ["q\"\\\n\t\u0001<"]}`

	for expr, want := range map[string]bool{
		`doc.o == '{"a": [1.50, "x"], "c": null, "bb": 1}'`: true,
		`doc.o == '{"a": [1.5, "x"], "c": null, "bb": 1}'`:  false,
		`doc.l == '["q\\"\\\\\\n\\t\\u0001<"]'`:             true,
	} {
		p, err := s.Compile(policy, filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)

		got, err := p.Evaluate(map[string]any{"doc": doc}, postgres.DefaultMaxDataBudget, nil)
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func TestArrayAliasesDoNotShadowColumnsOrRelations(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"tags": sqlbase.Array("tags", sqlbase.Text), "needle": sqlbase.Scalar("_aip_element_0", sqlbase.Text),
		"other": sqlbase.Scalar("_aip_element_1", sqlbase.Text), "qualified": sqlbase.Scalar("_aip_element_2.name", sqlbase.Text),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	p := compile(t, s, `tags.contains(needle) || tags.startsWith(other) || tags.endsWith(qualified)`)
	sql, args, err := p.SQL()
	require.NoError(t, err)
	require.Empty(t, args)

	for _, alias := range []string{`AS "_aip_element_3"`, `AS "_aip_element_4"`, `AS "_aip_element_5"`} {
		require.Contains(t, sql, alias)
	}

	for _, alias := range []string{`AS "_aip_element_0"`, `AS "_aip_element_1"`, `AS "_aip_element_2"`} {
		require.NotContains(t, sql, alias)
	}
	// Aliases are allocated per render, never stored in the immutable program.
	again, againArgs, err := p.SQL()
	require.NoError(t, err)
	require.Equal(t, sql, again)
	require.Equal(t, args, againArgs)

	inline, err := p.Inline()
	require.NoError(t, err)
	require.Equal(t, sql, inline)
}

func TestTextLiteralsCarryCCollation(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`"z" < "é"`, `["z"] < ["é"]`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			p := compile(t, schema(t), expr)
			sql, args, err := p.SQL()
			require.NoError(t, err)
			require.Equal(t, []any{"z", "é"}, args)
			require.Contains(t, sql, `::text COLLATE "C"`)

			inline, err := p.Inline()
			require.NoError(t, err)
			require.Contains(t, inline, `::text COLLATE "C"`)

			got, err := p.Evaluate(nil, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.True(t, got)
		})
	}
}

func TestJSONTimestampRounding(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"doc.ts": sqlbase.JSONPath("doc", sqlbase.Timestamp, "ts")})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	for _, tc := range []struct{ input, want string }{
		{"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z"},
		{"2024-01-01T00:00:00.0000004Z", "2024-01-01T00:00:00Z"},
		{"2024-01-01T00:00:00.0000005Z", "2024-01-01T00:00:00Z"},
		{"2024-01-01T00:00:00.0000009Z", "2024-01-01T00:00:00.000001Z"},
		{"2024-01-01T00:00:00.0000015Z", "2024-01-01T00:00:00.000002Z"},
		{"2024-01-01T00:00:00.0000025Z", "2024-01-01T00:00:00.000002Z"},
		{"2024-01-01T00:00:00.0000005000000001Z", "2024-01-01T00:00:00.000001Z"},
		{"2024-01-01T23:59:59.9999999Z", "2024-01-02T00:00:00Z"},
		{"1969-12-31T23:59:59.9999999Z", "1970-01-01T00:00:00Z"},
		{"2024-01-01T07:00:00.0000009+07:00", "2024-01-01T00:00:00.000001Z"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()

			got, present, err := s.Lookup(policy, map[string]any{"doc": map[string]any{"ts": tc.input}}, "doc.ts", postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.True(t, present)

			want, err := time.Parse(time.RFC3339Nano, tc.want)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

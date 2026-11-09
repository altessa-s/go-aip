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

func BenchmarkEvaluate(b *testing.B) {
	p := compile(b, schema(b), `n >= "0.1" && doc.n == 1.00000000000000001`)
	row := map[string]any{"n": "100.01", "doc": `{"n":1.00000000000000001}`}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = p.Evaluate(row, 1<<22, nil)
	}
}
func BenchmarkSQL(b *testing.B) {
	p := compile(b, schema(b), `n >= "0.1" && doc.n == 1.00000000000000001`)
	b.ReportAllocs()

	for b.Loop() {
		_, _, _ = p.SQL()
	}
}

func BenchmarkCompile(b *testing.B) {
	s := schema(b)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(b, err)
	node := filtertest.MustParseFilter(b, `n >= "0.1" && doc.n == 1.00000000000000001`)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = s.Compile(policy, node)
	}
}

func BenchmarkSQLArrayPredicate(b *testing.B) {
	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"tags": sqlbase.Array("tags", sqlbase.Text), "needle": sqlbase.Scalar("_aip_element_0", sqlbase.Text),
	})
	require.NoError(b, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(b, err)
	p := compile(b, s, `tags.contains(needle) == true`)
	b.ReportAllocs()

	for b.Loop() {
		_, _, _ = p.SQL()
	}
}

func BenchmarkJSONTimestampLookup(b *testing.B) {
	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"doc.ts": sqlbase.JSONPath("doc", sqlbase.Timestamp, "ts")})
	require.NoError(b, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(b, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(b, err)

	row := map[string]any{"doc": `{"ts":"2024-01-01T23:59:59.9999999Z"}`}

	b.ReportAllocs()

	for b.Loop() {
		_, _, _ = s.Lookup(policy, row, "doc.ts", postgres.DefaultMaxDataBudget, nil)
	}
}

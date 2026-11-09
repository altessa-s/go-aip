// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkInjectedComparator(b *testing.B) {
	e, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("name")), evaluator.WithComparator(foldingComparator{}))
	require.NoError(b, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "ADA"}}
	p, err := e.Prepare(n)
	require.NoError(b, err)

	data := map[string]any{"name": "Ada"}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = p.Evaluate(data)
	}
}

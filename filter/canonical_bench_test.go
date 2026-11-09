// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func BenchmarkCanonical(b *testing.B) {
	p, err := filterparser.New()
	require.NoError(b, err)
	node, err := p.Parse(b.Context(), `kind in ["a", "b"] && priority > 1 && name.startsWith("n") || !(note == null)`)
	require.NoError(b, err)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = filter.Canonical(node)
	}
}

func BenchmarkCanonical_Complex(b *testing.B) {
	node := benchTree(b)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = filter.Canonical(node)
	}
}

// BenchmarkCanonical_Deep encodes a chain of 32 nested conjunctions, where every level re-prefixes the encoding below it.
func BenchmarkCanonical_Deep(b *testing.B) {
	var node filter.Node = &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "a"}, Right: &filter.LiteralNode{Value: int64(0)}}
	for i := range 32 {
		node = &filter.BinaryOpNode{Op: filter.OpAnd, Left: node, Right: &filter.BinaryOpNode{
			Op: filter.OpGT, Left: &filter.IdentNode{Name: "b"}, Right: &filter.LiteralNode{Value: int64(i)},
		}}
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = filter.Canonical(node)
	}
}

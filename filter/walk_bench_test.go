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

// benchTree parses a 20-node filter mixing every built-in node kind.
func benchTree(b *testing.B) filter.Node {
	b.Helper()

	p, err := filterparser.New()
	require.NoError(b, err)
	node, err := p.Parse(b.Context(),
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"] && !(x == 1) && y.startsWith("a")`)
	require.NoError(b, err)

	return node
}

func BenchmarkWalk(b *testing.B) {
	node := benchTree(b)
	count := 0

	b.ReportAllocs()

	for b.Loop() {
		filter.Walk(node, func(filter.Node) bool { count++; return true })
	}
}

func BenchmarkDepth(b *testing.B) {
	node := benchTree(b)

	b.ReportAllocs()

	for b.Loop() {
		_ = filter.Depth(node)
	}
}

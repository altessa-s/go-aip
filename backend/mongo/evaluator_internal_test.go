// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"iter"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

type budgetNode struct {
	filter.Node
	children []filter.Node
}

func (n budgetNode) Children() iter.Seq[filter.Node] {
	return func(yield func(filter.Node) bool) {
		for _, c := range n.children {
			if !yield(c) {
				return
			}
		}
	}
}

func TestNodeCountCustomChildren(t *testing.T) {
	t.Parallel()

	node := budgetNode{children: []filter.Node{budgetNode{children: []filter.Node{&filter.LiteralNode{Value: true}}}}}
	require.Equal(t, 3, nodeCount(node))
}

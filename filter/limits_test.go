// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"iter"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

// wrapNode is a custom [filter.Node] with one child, walked through Children().
type wrapNode struct{ child filter.Node }

func (n *wrapNode) Kind() filter.NodeKind              { return filter.NodeKindUnaryOp }
func (n *wrapNode) Accept(filter.Visitor) (any, error) { return nil, nil }
func (n *wrapNode) Children() iter.Seq[filter.Node] {
	return func(yield func(filter.Node) bool) { yield(n.child) }
}

func (n *wrapNode) String() string { return "wrap" }

// TestCheckLimits pins how CheckLimits walks each node type: every node takes an operation, every compound node a level of
// depth; a nil operand or call target is skipped while a nil list element or call argument fails; a custom node is walked
// through Children().
func TestCheckLimits(t *testing.T) {
	t.Parallel()

	lit := func(v any) filter.Node { return &filter.LiteralNode{Value: v} }
	id := &filter.IdentNode{Name: "a"}

	eq := &filter.BinaryOpNode{Op: filter.OpEqual, Left: id, Right: lit(int64(1))}
	for _, tc := range []struct {
		name  string
		node  filter.Node
		ops   int
		depth int
		want  error
	}{
		{"binary within", eq, 3, 1, nil},
		{"binary ops", eq, 2, 5, filter.ErrMaxOperationsExceeded},
		{"binary depth", &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq, Right: eq}, 10, 1, filter.ErrMaxDepthExceeded},
		{"nil operand skipped", &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq}, 4, 2, nil},
		{"unary", &filter.UnaryOpNode{Op: filter.OpNot, Operand: eq}, 4, 2, nil},
		{"unary depth", &filter.UnaryOpNode{Op: filter.OpNot, Operand: eq}, 10, 1, filter.ErrMaxDepthExceeded},
		{"nil unary operand", &filter.UnaryOpNode{Op: filter.OpNot}, 1, 1, nil},
		{"call", &filter.CallNode{Op: filter.OpContains, Target: id, Args: []filter.Node{lit("x")}}, 3, 1, nil},
		{"call ops", &filter.CallNode{Op: filter.OpContains, Target: id, Args: []filter.Node{lit("x")}}, 2, 1, filter.ErrMaxOperationsExceeded},
		{"nil call target", &filter.CallNode{Op: filter.OpContains, Args: []filter.Node{lit("x")}}, 2, 1, nil},
		{"nil call argument", &filter.CallNode{Op: filter.OpContains, Target: id, Args: []filter.Node{nil}}, 5, 5, filter.ErrInvalidExpression},
		{"list", &filter.ListNode{Elements: []filter.Node{lit(int64(1)), lit(int64(2))}}, 3, 1, nil},
		{"nested list depth", &filter.ListNode{Elements: []filter.Node{&filter.ListNode{}}}, 5, 1, filter.ErrMaxDepthExceeded},
		{"nil list element", &filter.ListNode{Elements: []filter.Node{nil}}, 5, 5, filter.ErrInvalidExpression},
		{"typed nil", &filter.BinaryOpNode{Op: filter.OpAnd, Left: (*filter.LiteralNode)(nil), Right: eq}, 10, 5, filter.ErrInvalidExpression},
		{"custom node", &wrapNode{child: eq}, 4, 2, nil},
		{"custom node depth", &wrapNode{child: eq}, 10, 1, filter.ErrMaxDepthExceeded},
		{"custom node ops", &wrapNode{child: eq}, 3, 5, filter.ErrMaxOperationsExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithMaxOperations(tc.ops), filter.WithMaxDepth(tc.depth))
			require.NoError(t, err)

			err = ctx.CheckLimits(tc.node)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestNegationNormalFormCustomNode(t *testing.T) {
	t.Parallel()

	node := &wrapNode{child: &filter.IdentNode{Name: "active"}}
	got := filter.NegationNormalForm(&filter.UnaryOpNode{Op: filter.OpNot, Operand: node}, nil, nil)
	require.Equal(t, &filter.UnaryOpNode{Op: filter.OpNot, Operand: node}, got)
}

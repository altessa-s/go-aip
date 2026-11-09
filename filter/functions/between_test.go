// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	filter "github.com/altessa-s/go-aip/filter"
	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func TestBetween_Expansion(t *testing.T) {
	t.Parallel()

	field := &filter.IdentNode{Name: "age"}
	lo := &filter.LiteralNode{Value: int64(18)}
	hi := &filter.LiteralNode{Value: int64(65)}

	node, err := filterfuncs.Between()([]filter.Node{field, lo, hi})
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok, "expected BinaryOpNode, got %T", node)
	require.Equal(t, filter.OpAnd, andNode.Op)

	left, ok := andNode.Left.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpGTE, left.Op)
	require.Same(t, field, left.Left)
	require.Same(t, lo, left.Right)

	right, ok := andNode.Right.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpLTE, right.Op)
	require.Same(t, field, right.Left)
	require.Same(t, hi, right.Right)
}

func TestBetween_WrongArity(t *testing.T) {
	t.Parallel()

	field := &filter.IdentNode{Name: "age"}
	lit := &filter.LiteralNode{Value: int64(1)}

	cases := [][]filter.Node{
		nil,
		{field},
		{field, lit},
		{field, lit, lit, lit},
	}
	for _, args := range cases {
		_, err := filterfuncs.Between()(args)
		require.ErrorIs(t, err, filter.ErrInvalidExpression, "args=%v", args)
	}
}

func TestBetween_FirstArgMustBeField(t *testing.T) {
	t.Parallel()

	args := []filter.Node{
		&filter.LiteralNode{Value: "not-a-field"},
		&filter.LiteralNode{Value: int64(1)},
		&filter.LiteralNode{Value: int64(2)},
	}
	_, err := filterfuncs.Between()(args)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "field identifier")
}

func TestBetweenFilter_OneEntry(t *testing.T) {
	t.Parallel()

	got := filterfuncs.BetweenFilter()
	require.Len(t, got, 1)
	require.NotNil(t, got[filterfuncs.BetweenFunc])
}

func TestBetween_ParserPickup(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(filterfuncs.BetweenFilter()))
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `between(age, 18, 65) && active`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpAnd, andNode.Op)

	// Left side: the expanded between(age, 18, 65) — itself an And-tree.
	betweenAnd, ok := andNode.Left.(*filter.BinaryOpNode)
	require.True(t, ok, "expected expanded BinaryOpNode on left, got %T", andNode.Left)
	require.Equal(t, filter.OpAnd, betweenAnd.Op)
	require.Equal(t, filter.OpGTE, betweenAnd.Left.(*filter.BinaryOpNode).Op)
	require.Equal(t, "age", betweenAnd.Left.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name)
	require.Equal(t, filter.OpLTE, betweenAnd.Right.(*filter.BinaryOpNode).Op)
}

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

func TestConstant_Expansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		field     string
		op        filter.Operator
		value     any
		wantOp    filter.Operator
		wantField string
		wantValue any
	}{
		{"string equal", "status", filter.OpEqual, "active", filter.OpEqual, "status", "active"},
		{"int gt", "failures", filter.OpGT, int64(0), filter.OpGT, "failures", int64(0)},
		{"float lte", "score", filter.OpLTE, 0.95, filter.OpLTE, "score", 0.95},
		{"bool ne", "active", filter.OpNotEqual, true, filter.OpNotEqual, "active", true},
		{"null eq", "deletedAt", filter.OpEqual, nil, filter.OpEqual, "deletedAt", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := filterfuncs.Constant(tt.field, tt.op, tt.value)(nil)
			require.NoError(t, err)

			bin, ok := node.(*filter.BinaryOpNode)
			require.True(t, ok, "expected BinaryOpNode, got %T", node)
			require.Equal(t, tt.wantOp, bin.Op)
			require.Equal(t, tt.wantField, bin.Left.(*filter.IdentNode).Name)
			require.Equal(t, tt.wantValue, bin.Right.(*filter.LiteralNode).Value)
		})
	}
}

func TestConstant_RejectsArgs(t *testing.T) {
	t.Parallel()

	h := filterfuncs.Constant("status", filter.OpEqual, "active")
	_, err := h([]filter.Node{&filter.LiteralNode{Value: "x"}})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "0 arguments")
}

func TestConstant_NonComparisonOp(t *testing.T) {
	t.Parallel()

	tests := []filter.Operator{filter.OpAnd, filter.OpOr, filter.OpNot, filter.OpIn, filter.OpContains}
	for _, op := range tests {
		t.Run(op.String(), func(t *testing.T) {
			_, err := filterfuncs.Constant("status", op, "x")(nil)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Contains(t, err.Error(), "not a comparison")
		})
	}
}

func TestConstant_ParserPickup(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
		"isActive":    filterfuncs.Constant("status", filter.OpEqual, "active"),
		"hasFailures": filterfuncs.Constant("failures", filter.OpGT, int64(0)),
	}))
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `isActive() && hasFailures()`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpAnd, andNode.Op)

	left := andNode.Left.(*filter.BinaryOpNode)
	require.Equal(t, filter.OpEqual, left.Op)
	require.Equal(t, "status", left.Left.(*filter.IdentNode).Name)
	require.Equal(t, "active", left.Right.(*filter.LiteralNode).Value)

	right := andNode.Right.(*filter.BinaryOpNode)
	require.Equal(t, filter.OpGT, right.Op)
	require.Equal(t, "failures", right.Left.(*filter.IdentNode).Name)
	require.Equal(t, int64(0), right.Right.(*filter.LiteralNode).Value)
}

func TestTextSearchOperator(t *testing.T) {
	t.Parallel()

	expand := filterfuncs.TextSearch([]string{"display_name"}, filterfuncs.WithOperator(filter.OpContains))
	node, err := expand([]filter.Node{&filter.LiteralNode{Value: "Иван"}})
	require.NoError(t, err)

	call := node.(*filter.CallNode)
	require.Equal(t, filter.OpContains, call.Op)
	require.Equal(t, "Иван", call.Args[0].(*filter.LiteralNode).Value)
}

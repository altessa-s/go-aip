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

func TestSoftDeleteFilters_NameSet(t *testing.T) {
	t.Parallel()

	got := filterfuncs.SoftDeleteFilters()
	require.Len(t, got, 2)
	require.NotNil(t, got[filterfuncs.SoftDeleteFuncNotDeleted])
	require.NotNil(t, got[filterfuncs.SoftDeleteFuncOnlyDeleted])
}

func TestSoftDeleteFilters_Expansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fn     string
		wantOp filter.Operator
	}{
		{filterfuncs.SoftDeleteFuncNotDeleted, filter.OpEqual},
		{filterfuncs.SoftDeleteFuncOnlyDeleted, filter.OpNotEqual},
	}

	funcs := filterfuncs.SoftDeleteFilters()

	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			node, err := funcs[tt.fn](nil)
			require.NoError(t, err)

			bin, ok := node.(*filter.BinaryOpNode)
			require.True(t, ok, "expected BinaryOpNode, got %T", node)
			require.Equal(t, tt.wantOp, bin.Op)

			ident, ok := bin.Left.(*filter.IdentNode)
			require.True(t, ok)
			require.Equal(t, filterfuncs.TimestampFieldDeletedAt, ident.Name)

			lit, ok := bin.Right.(*filter.LiteralNode)
			require.True(t, ok)
			require.Nil(t, lit.Value, "right side must be the null literal")
		})
	}
}

func TestSoftDeleteFilters_RejectArgs(t *testing.T) {
	t.Parallel()

	funcs := filterfuncs.SoftDeleteFilters()

	for _, name := range []string{filterfuncs.SoftDeleteFuncNotDeleted, filterfuncs.SoftDeleteFuncOnlyDeleted} {
		t.Run(name, func(t *testing.T) {
			_, err := funcs[name]([]filter.Node{&filter.LiteralNode{Value: "x"}})
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
		})
	}
}

func TestSoftDeleteFilters_FreshMapPerCall(t *testing.T) {
	t.Parallel()

	first := filterfuncs.SoftDeleteFilters()
	delete(first, filterfuncs.SoftDeleteFuncNotDeleted)

	second := filterfuncs.SoftDeleteFilters()
	require.NotNil(t, second[filterfuncs.SoftDeleteFuncNotDeleted],
		"each call must return a fresh map")
}

func TestSoftDeleteFilters_ParserPickup(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(filterfuncs.SoftDeleteFilters()))
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `notDeleted() && status == "active"`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpAnd, andNode.Op)

	left, ok := andNode.Left.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpEqual, left.Op)
	require.Equal(t, filterfuncs.TimestampFieldDeletedAt, left.Left.(*filter.IdentNode).Name)
	require.Nil(t, left.Right.(*filter.LiteralNode).Value)
}

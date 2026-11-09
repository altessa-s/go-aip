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

func TestTimestampFilters_NameSet(t *testing.T) {
	t.Parallel()

	got := filterfuncs.TimestampFilters()
	wantNames := []string{
		filterfuncs.TimestampFuncCreateAfter, filterfuncs.TimestampFuncCreateBefore,
		filterfuncs.TimestampFuncUpdateAfter, filterfuncs.TimestampFuncUpdateBefore,
		filterfuncs.TimestampFuncDeleteAfter, filterfuncs.TimestampFuncDeleteBefore,
	}
	require.Len(t, got, len(wantNames))

	for _, name := range wantNames {
		require.NotNil(t, got[name], "missing handler for %q", name)
	}
}

func TestTimestampFilters_Expansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fn        string
		wantField string
		wantOp    filter.Operator
	}{
		{filterfuncs.TimestampFuncCreateAfter, filterfuncs.TimestampFieldCreatedAt, filter.OpGT},
		{filterfuncs.TimestampFuncCreateBefore, filterfuncs.TimestampFieldCreatedAt, filter.OpLT},
		{filterfuncs.TimestampFuncUpdateAfter, filterfuncs.TimestampFieldUpdatedAt, filter.OpGT},
		{filterfuncs.TimestampFuncUpdateBefore, filterfuncs.TimestampFieldUpdatedAt, filter.OpLT},
		{filterfuncs.TimestampFuncDeleteAfter, filterfuncs.TimestampFieldDeletedAt, filter.OpGT},
		{filterfuncs.TimestampFuncDeleteBefore, filterfuncs.TimestampFieldDeletedAt, filter.OpLT},
	}

	funcs := filterfuncs.TimestampFilters()
	arg := &filter.LiteralNode{Value: "2024-01-01"}

	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			node, err := funcs[tt.fn]([]filter.Node{arg})
			require.NoError(t, err)

			bin, ok := node.(*filter.BinaryOpNode)
			require.True(t, ok, "expected BinaryOpNode, got %T", node)
			require.Equal(t, tt.wantOp, bin.Op)

			ident, ok := bin.Left.(*filter.IdentNode)
			require.True(t, ok, "Left is not IdentNode, got %T", bin.Left)
			require.Equal(t, tt.wantField, ident.Name)
			require.Same(t, arg, bin.Right, "argument must pass through unchanged")
		})
	}
}

func TestTimestampFilters_FreshMapPerCall(t *testing.T) {
	t.Parallel()

	first := filterfuncs.TimestampFilters()
	delete(first, filterfuncs.TimestampFuncCreateAfter)

	second := filterfuncs.TimestampFilters()
	require.NotNil(t, second[filterfuncs.TimestampFuncCreateAfter],
		"each call must return a fresh map; mutating one must not affect another")
}

func TestTimestampFilters_NotRegisteredByDefault(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	for _, expr := range []string{
		`createAfter("2024-01-01")`,
		`createBefore("2024-01-01")`,
		`updateAfter("2024-01-01")`,
		`updateBefore("2024-01-01")`,
		`deleteAfter("2024-01-01")`,
		`deleteBefore("2024-01-01")`,
	} {
		t.Run(expr, func(t *testing.T) {
			_, err := p.Parse(t.Context(), expr)
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation,
				"timestamp filters must be opt-in, not auto-registered")
		})
	}
}

func TestTimestampFilters_GlobalRegistration(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(filterfuncs.TimestampFilters()))
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `createAfter("2024-01-01") && deleteBefore("2024-12-31")`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok)
	require.Equal(t, filter.OpAnd, andNode.Op)
	require.Equal(t, filter.OpGT, andNode.Left.(*filter.BinaryOpNode).Op)
	require.Equal(t, filterfuncs.TimestampFieldCreatedAt, andNode.Left.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name)
	require.Equal(t, filter.OpLT, andNode.Right.(*filter.BinaryOpNode).Op)
	require.Equal(t, filterfuncs.TimestampFieldDeletedAt, andNode.Right.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name)
}

func TestSelectTimestampFilters_Subset(t *testing.T) {
	t.Parallel()

	got := filterfuncs.SelectTimestampFilters(
		filterfuncs.TimestampFuncCreateAfter,
		filterfuncs.TimestampFuncUpdateBefore,
	)

	require.Len(t, got, 2)
	require.NotNil(t, got[filterfuncs.TimestampFuncCreateAfter])
	require.NotNil(t, got[filterfuncs.TimestampFuncUpdateBefore])
	require.Nil(t, got[filterfuncs.TimestampFuncDeleteAfter])
}

func TestSelectTimestampFilters_UnknownSkipped(t *testing.T) {
	t.Parallel()

	got := filterfuncs.SelectTimestampFilters(
		filterfuncs.TimestampFuncCreateAfter,
		"unknownTimestamp",
		"createdAt", // a field name, not a function name
	)

	require.Len(t, got, 1)
	require.NotNil(t, got[filterfuncs.TimestampFuncCreateAfter])
}

func TestSelectTimestampFilters_Empty(t *testing.T) {
	t.Parallel()
	require.Empty(t, filterfuncs.SelectTimestampFilters())
}

func TestSelectTimestampFilters_DuplicateNames(t *testing.T) {
	t.Parallel()

	got := filterfuncs.SelectTimestampFilters(
		filterfuncs.TimestampFuncCreateAfter,
		filterfuncs.TimestampFuncCreateAfter,
	)
	require.Len(t, got, 1)
}

func TestSelectTimestampFilters_ParserPickup(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(filterfuncs.SelectTimestampFilters(
		filterfuncs.TimestampFuncCreateAfter,
		filterfuncs.TimestampFuncCreateBefore,
	)))
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `createAfter("2024-01-01")`)
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `deleteAfter("2024-01-01")`)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation,
		"unselected entries must not be registered")
}

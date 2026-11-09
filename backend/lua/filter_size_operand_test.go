// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// TestSizeOutsideComparison pins size() where it is neither a predicate nor the left operand of a comparison. The translator
// used to render its target instead, so `size(name) in [1]` tested name itself.
func TestSizeOutsideComparison(t *testing.T) {
	t.Parallel()

	size := func(target filter.Node) *filter.CallNode { return &filter.CallNode{Op: filter.OpSize, Target: target} }
	name := &filter.IdentNode{Name: "name"}
	one := &filter.LiteralNode{Value: int64(1)}

	tests := []struct {
		name string
		expr string
		node filter.Node
		want error
	}{
		{name: "bare", expr: `size(name)`, want: filter.ErrInvalidExpression},
		{name: "negated", expr: `!size(name)`, want: filter.ErrInvalidExpression},
		{name: "logical operand", expr: `size(name) && flag`, want: filter.ErrInvalidExpression},
		{name: "in list", expr: `size(name) in [1, 2]`, want: filter.ErrUnsupportedOperation},
		{name: "negated in list", expr: `!(size(name) in [1])`, want: filter.ErrUnsupportedOperation},
		{name: "string function target", expr: `size(name).contains("a")`, want: filter.ErrUnsupportedOperation},
		{name: "nested size", expr: `size(size(name)) == 1`, want: filter.ErrUnsupportedOperation},
		{name: "has target", node: &filter.CallNode{Op: filter.OpHas, Target: size(name)}, want: filter.ErrUnsupportedOperation},
		{
			name: "substring target",
			node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpSubstring, Target: size(name), Args: []filter.Node{one}}, Right: one},
			want: filter.ErrUnsupportedOperation,
		},
	}

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	translator, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := tt.node
			if node == nil {
				parsed, perr := parser.Parse(t.Context(), tt.expr)
				require.NoError(t, perr)

				node = parsed
			}

			script, err := translator.Translate(node)
			require.ErrorIs(t, err, tt.want)
			require.Empty(t, script)
		})
	}
}

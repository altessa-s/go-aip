// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

// TestCompileMalformedNodes pins the errors of AST shapes a validated filter never has: the compiled program reports them as
// filter.ErrInvalidExpression, at evaluation time.
func TestCompileMalformedNodes(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"id"}, listing.NewProfile())

	id := &filter.IdentNode{Name: "id"}
	lit := &filter.LiteralNode{Value: "a"}

	tests := []struct {
		name string
		node filter.Node
		want string
	}{
		{name: "unsupported node", node: lit, want: "unsupported filter node *filter.LiteralNode: invalid expression structure"},
		{name: "function target", node: &filter.CallNode{Op: filter.OpContains, Target: lit}, want: "function target is *filter.LiteralNode: invalid expression structure"},
		{name: "size outside a comparison", node: &filter.CallNode{Op: filter.OpSize, Target: id}, want: "size() is only valid in a comparison: invalid expression structure"},
		{name: "string function arity", node: &filter.CallNode{Op: filter.OpContains, Target: id}, want: "string function requires one argument: invalid expression structure"},
		{
			name: "string function operand", node: &filter.CallNode{Op: filter.OpContains, Target: id, Args: []filter.Node{id}},
			want: "filter operand is *filter.IdentNode: invalid expression structure",
		},
		{
			name: "string function argument type",
			node: &filter.CallNode{Op: filter.OpContains, Target: id, Args: []filter.Node{&filter.LiteralNode{Value: int64(1)}}},
			want: "string function argument is int64: invalid expression structure",
		},
		{
			name: "size target", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpSize, Target: lit}, Right: lit},
			want: "size() target is *filter.LiteralNode: invalid expression structure",
		},
		{name: "comparison field", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: lit, Right: lit}, want: "comparison field is *filter.LiteralNode: invalid expression structure"},
		{name: "comparison operand", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: id, Right: id}, want: "filter operand is *filter.IdentNode: invalid expression structure"},
		{
			name: "list operand element", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: id, Right: &filter.ListNode{Elements: []filter.Node{id}}},
			want: "filter operand is *filter.IdentNode: invalid expression structure",
		},
		{name: "in field", node: &filter.BinaryOpNode{Op: filter.OpIn, Left: lit, Right: lit}, want: "in field is *filter.LiteralNode: invalid expression structure"},
		{name: "in list", node: &filter.BinaryOpNode{Op: filter.OpIn, Left: id, Right: lit}, want: "in list is *filter.LiteralNode: invalid expression structure"},
		{
			name: "in array operand", node: &filter.BinaryOpNode{Op: filter.OpIn, Left: id, Right: &filter.IdentNode{Name: "tags"}},
			want: "filter operand is *filter.IdentNode: invalid expression structure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			profile, err := New(schema, NewReader(nil, 0))
			require.NoError(t, err)

			_, err = profile.CompileEvaluator(schema.FilterPolicy(), tt.node)
			require.Error(t, err, "filter.Validate must reject the shape before compilation")

			c := compiler{schema: schema, policy: schema.FilterPolicy(), matchers: map[string]filter.Regex{}, refs: map[string]*fieldRef{}}
			e := contractEvaluator{reader: NewReader(nil, 0), budget: readBudget{left: 100}, bounded: true}

			got, err := e.matches(c.compile(tt.node), map[string]any{"id": "a", "tags": []string{"a"}})
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.EqualError(t, err, tt.want)
			require.Equal(t, falseValue, got)
		})
	}
}

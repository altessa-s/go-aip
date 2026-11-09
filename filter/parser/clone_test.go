// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	filter "github.com/altessa-s/go-aip/filter"
)

// TestParse_HitCopyMatchesClone pins PERF-12 (b): a cache hit is slab-copied, equal to the [filter.Clone] of the cached
// tree and sharing no node, child slice or byte literal with it.
func TestParse_HitCopyMatchesClone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "comparison", expr: `name == "John"`},
		{name: "logical and list", expr: `(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`},
		{name: "unary", expr: `!(a == 1) && !b`},
		{name: "method call", expr: `name.startsWith("J") && name.contains("o")`},
		{name: "global call", expr: `size(tags) > 2 && has(a.b)`},
		{name: "bytes literal", expr: `data == b"\x01\x02"`},
		{name: "timestamp", expr: `create_time > timestamp("2024-01-01T00:00:00Z")`},
		{name: "empty list", expr: `a in []`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New()
			require.NoError(t, err)

			shared, err := p.ParseShared(t.Context(), tt.expr)
			require.NoError(t, err)

			hit, ok := p.cache.get(tt.expr)
			require.True(t, ok)
			require.NotNil(t, hit.plan, "a parsed tree is slab-copyable")

			want, err := filter.Clone(shared)
			require.NoError(t, err)

			owned, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err)
			require.Equal(t, want, owned)
			requireDisjoint(t, shared, owned)
		})
	}
}

// requireDisjoint fails when two trees of the same shape share a node, a child slice's backing array or a byte literal.
func requireDisjoint(t *testing.T, a, b filter.Node) {
	t.Helper()

	if a == nil || b == nil {
		require.Equal(t, a == nil, b == nil)
		return
	}

	require.NotSame(t, a, b)

	switch a := a.(type) {
	case *filter.LiteralNode:
		if ab, ok := a.Value.([]byte); ok && len(ab) > 0 {
			bb, _ := b.(*filter.LiteralNode).Value.([]byte) //nolint:forcetypeassert // same shape
			require.NotSame(t, &ab[0], &bb[0])
		}
	case *filter.BinaryOpNode:
		bn := b.(*filter.BinaryOpNode) //nolint:forcetypeassert // same shape
		requireDisjoint(t, a.Left, bn.Left)
		requireDisjoint(t, a.Right, bn.Right)
	case *filter.UnaryOpNode:
		requireDisjoint(t, a.Operand, b.(*filter.UnaryOpNode).Operand) //nolint:forcetypeassert // same shape
	case *filter.CallNode:
		bn := b.(*filter.CallNode) //nolint:forcetypeassert // same shape
		requireDisjoint(t, a.Target, bn.Target)
		requireDisjointAll(t, a.Args, bn.Args)
	case *filter.ListNode:
		requireDisjointAll(t, a.Elements, b.(*filter.ListNode).Elements) //nolint:forcetypeassert // same shape
	}
}

func requireDisjointAll(t *testing.T, a, b []filter.Node) {
	t.Helper()

	require.Len(t, b, len(a))

	if len(a) > 0 {
		require.NotSame(t, &a[0], &b[0])
	}

	for i := range a {
		requireDisjoint(t, a[i], b[i])
	}
}

// TestParse_HitCopyAppendIsolated keeps the slab copy's child slices capacity-capped: appending to one cannot overwrite a
// sibling's children.
func TestParse_HitCopyAppendIsolated(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	const expr = `a in [1, 2] && b in [3, 4]`

	_, err = p.ParseShared(t.Context(), expr)
	require.NoError(t, err)

	owned, err := p.Parse(t.Context(), expr)
	require.NoError(t, err)

	root := owned.(*filter.BinaryOpNode)                                //nolint:forcetypeassert // shape of expr
	left := root.Left.(*filter.BinaryOpNode).Right.(*filter.ListNode)   //nolint:forcetypeassert // shape of expr
	right := root.Right.(*filter.BinaryOpNode).Right.(*filter.ListNode) //nolint:forcetypeassert // shape of expr

	left.Elements = append(left.Elements, &filter.LiteralNode{Value: int64(99), Text: "99"})

	require.Len(t, right.Elements, 2)
	require.Equal(t, int64(3), right.Elements[0].(*filter.LiteralNode).Value) //nolint:forcetypeassert // shape of expr

	again, err := p.Parse(t.Context(), expr)
	require.NoError(t, err)
	require.Len(t, again.(*filter.BinaryOpNode).Left.(*filter.BinaryOpNode).Right.(*filter.ListNode).Elements, 2) //nolint:forcetypeassert // shape of expr
}

type customNode struct{ filter.IdentNode }

// mutableValue is a literal type holding a pointer, which a copy would alias.
type mutableValue struct{ P *int }

func TestNewClonePlan(t *testing.T) {
	t.Parallel()

	shared := &filter.IdentNode{Name: "a"}

	tests := []struct {
		name string
		root filter.Node
		want *clonePlan
	}{
		{name: "nil", root: nil, want: &clonePlan{}},
		{
			name: "every kind",
			root: &filter.BinaryOpNode{
				Op: filter.OpAnd,
				Left: &filter.CallNode{Op: filter.OpContains, Target: &filter.IdentNode{Name: "s"}, Args: []filter.Node{
					&filter.LiteralNode{Value: []byte("x")},
				}},
				Right: &filter.UnaryOpNode{Op: filter.OpNot, Operand: &filter.ListNode{Elements: []filter.Node{
					&filter.LiteralNode{Value: time.Unix(0, 0)}, &filter.LiteralNode{Value: "s"},
				}}},
			},
			want: &clonePlan{binary: 1, unary: 1, call: 1, list: 1, ident: 1, literal: 3, children: 3},
		},
		{name: "shared node", root: &filter.BinaryOpNode{Op: filter.OpEqual, Left: shared, Right: shared}},
		{name: "custom node", root: &customNode{}},
		{name: "typed nil", root: &filter.UnaryOpNode{Op: filter.OpNot, Operand: (*filter.IdentNode)(nil)}},
		{name: "typed nil literal", root: (*filter.LiteralNode)(nil)},
		{name: "mutable literal", root: &filter.LiteralNode{Value: mutableValue{}}},
		{name: "typed nil binary", root: (*filter.BinaryOpNode)(nil)},
		{name: "typed nil call", root: (*filter.CallNode)(nil)},
		{name: "typed nil list", root: (*filter.ListNode)(nil)},
		{name: "typed nil unary", root: (*filter.UnaryOpNode)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, newClonePlan(tt.root))
		})
	}
}

// TestParse_HitWithoutPlanFallsBackToClone covers a cached tree the slab copy does not handle.
func TestParse_HitWithoutPlanFallsBackToClone(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	const expr = `a == 1`

	shared, err := p.ParseShared(t.Context(), expr)
	require.NoError(t, err)

	p.cache.entries.Put(expr, astEntry{node: shared})

	owned, err := p.Parse(t.Context(), expr)
	require.NoError(t, err)
	require.Equal(t, shared, owned)
	requireDisjoint(t, shared, owned)
}

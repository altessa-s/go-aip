// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func TestNegationNormalForm(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New()
	require.NoError(t, err)

	cases := []struct {
		before, after      string
		arrays, invertible map[string]bool
	}{
		{`!(a && b)`, `(a == false || b == false)`, nil, map[string]bool{"a": true, "b": true}},
		{`!!(x < 2)`, `x < 2`, nil, map[string]bool{"x": true}},
		{`!(x < 2)`, `x >= 2`, nil, map[string]bool{"x": true}},
		{`!has(x.value)`, `x.value == null`, nil, map[string]bool{"x.value": true}},
		{`!active`, `active == false`, nil, map[string]bool{"active": true}},
		{`!(tags == "a")`, `!(tags == "a")`, map[string]bool{"tags": true}, map[string]bool{"tags": true}},
		{`!(name.startsWith("a"))`, `!(name.startsWith("a"))`, nil, map[string]bool{"name": true}},
		{`!(age > 10)`, `!(age > 10)`, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.before, func(t *testing.T) {
			t.Parallel()
			before, err := p.Parse(t.Context(), tc.before)
			require.NoError(t, err)
			want, err := p.Parse(t.Context(), tc.after)
			require.NoError(t, err)

			got := filter.NegationNormalForm(before, tc.arrays, tc.invertible)
			require.Equal(t, mustCanonical(t, want), mustCanonical(t, got))
			require.Equal(t, mustCanonical(t, got), mustCanonical(t, filter.NegationNormalForm(got, tc.arrays, tc.invertible)))
		})
	}
}

func TestNegationNormalFormKleeneProperty(t *testing.T) {
	t.Parallel()
	// Small deterministic random corpus over nullable boolean leaves.
	rng := rand.New(rand.NewSource(1))
	rows := []map[string]any{{"a": true, "b": false}, {"a": false, "b": true}, {"a": nil, "b": true}, {"a": false, "b": nil}, {"a": nil, "b": nil}}

	var leaves []filter.Node
	for _, name := range []string{"a", "b"} {
		leaves = append(leaves, &filter.IdentNode{Name: name})
	}

	var build func(int) filter.Node

	build = func(depth int) filter.Node {
		if depth == 0 || rng.Intn(4) == 0 {
			return leaves[rng.Intn(len(leaves))]
		}

		if rng.Intn(3) == 0 {
			return &filter.UnaryOpNode{Op: filter.OpNot, Operand: build(depth - 1)}
		}

		op := filter.OpAnd
		if rng.Intn(2) == 0 {
			op = filter.OpOr
		}

		return &filter.BinaryOpNode{Op: op, Left: build(depth - 1), Right: build(depth - 1)}
	}
	for range 100 {
		node := build(5)

		got := filter.NegationNormalForm(node, nil, map[string]bool{"a": true, "b": true})
		for _, row := range rows {
			require.Equal(t, evalKleeneNode(t, node, row), evalKleeneNode(t, got, row))
		}
	}
}

func evalKleeneNode(t *testing.T, node filter.Node, row map[string]any) int {
	t.Helper()

	switch n := node.(type) {
	case *filter.IdentNode:
		if n, ok := row[n.Name]; ok && n == nil {
			return 0
		}

		if row[n.Name] == true {
			return 1
		}

		return -1
	case *filter.UnaryOpNode:
		v := evalKleeneNode(t, n.Operand, row)
		if v == 0 {
			return 0
		}

		return -v
	case *filter.BinaryOpNode:
		if n.Op == filter.OpEqual || n.Op == filter.OpNotEqual {
			ident, ok := n.Left.(*filter.IdentNode)
			require.True(t, ok)

			value := row[ident.Name]
			lit, ok := n.Right.(*filter.LiteralNode)
			require.True(t, ok)

			if value == nil {
				return 0
			}

			equal := value == lit.Value
			if n.Op == filter.OpNotEqual {
				equal = !equal
			}

			if equal {
				return 1
			}

			return -1
		}

		left, right := evalKleeneNode(t, n.Left, row), evalKleeneNode(t, n.Right, row)
		if n.Op == filter.OpAnd {
			if left == -1 || right == -1 {
				return -1
			}

			if left == 0 || right == 0 {
				return 0
			}

			return 1
		}

		if left == 1 || right == 1 {
			return 1
		}

		if left == 0 || right == 0 {
			return 0
		}

		return -1
	default:
		t.Fatalf("unsupported node %T", node)
		return 0
	}
}

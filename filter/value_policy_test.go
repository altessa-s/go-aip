// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/filter/parser"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

type literalValidationCodec struct{ err error }

func (c literalValidationCodec) ValidateLiteral(any) error { return c.err }

func TestMutableEnumRejected(t *testing.T) {
	t.Parallel()

	state := "active"
	for _, value := range []any{&state, struct{ P *string }{&state}} {
		_, err := parser.New(parser.WithEnumNames(map[string]map[string]any{"state": {"ACTIVE": value}}))
		require.ErrorIs(t, err, filter.ErrInvalidOptions)
		_, err = filter.Clone(&filter.LiteralNode{Value: value})
		require.ErrorIs(t, err, filter.ErrUnsupportedType)
	}
}

func TestCanonicalAggregateLiterals(t *testing.T) {
	t.Parallel()

	type pair struct{ A, B string }

	a, err := filter.Canonical(&filter.LiteralNode{Value: pair{"a b", "c"}})
	require.NoError(t, err)
	b, err := filter.Canonical(&filter.LiteralNode{Value: pair{"a", "b c"}})
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	typed := pagetoken.NewTyped[string](pagetokentest.Codec(t))
	token, err := typed.Issue("position", pagetoken.NewBindings("list", "id", a))
	require.NoError(t, err)
	_, _, err = typed.Resolve(token, pagetoken.NewBindings("list", "id", b))
	require.Error(t, err)
}

func TestComparatorMembership(t *testing.T) {
	t.Parallel()

	for _, array := range []bool{false, true} {
		opts := []filter.PolicyOption{filter.WithAllowedFields("state")}
		data := map[string]any{"state": "ACTIVE"}

		if array {
			opts = append(opts, filter.WithArrayFields("state"))
			data["state"] = []any{"ACTIVE"}
		}

		e, err := evaluator.New(filtertest.MustPolicy(t, opts...), evaluator.WithComparator(foldingComparator{}))
		require.NoError(t, err)
		p, err := parser.New()
		require.NoError(t, err)
		n, err := p.Parse(t.Context(), `state in ["active"]`)
		require.NoError(t, err)
		match, err := e.Evaluate(n, data)
		require.NoError(t, err)
		require.True(t, match)
	}
}

func TestValueCodecAndEnumPolicy(t *testing.T) {
	t.Parallel()

	for _, kind := range []filter.FieldKind{filter.FieldKindInt, filter.FieldKindDecimal} {
		p, err := filter.NewPolicy(filter.WithAllowedFields("v"), filter.WithFieldTypes(map[string]filter.FieldKind{"v": kind}), filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: literalValidationCodec{err: filter.ErrUnsupportedType}}))
		require.NoError(t, err)
		require.ErrorIs(t, p.CheckLiteralKind("v", &filter.LiteralNode{Value: int64(2)}), filter.ErrUnsupportedType)
		p, err = filter.NewPolicy(filter.WithAllowedFields("v"), filter.WithFieldTypes(map[string]filter.FieldKind{"v": kind}), filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: literalValidationCodec{}}), filter.WithEnumValues(map[string][]int64{"v": {1}}))
		require.NoError(t, err)
		require.ErrorIs(t, p.CheckLiteralKind("v", &filter.ListNode{Elements: []filter.Node{&filter.LiteralNode{Value: int64(2)}}}), filter.ErrEnumValueNotAllowed)
	}
}

func TestEvaluatorPolicyLimits(t *testing.T) {
	t.Parallel()

	e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("state"), filter.WithMaxDepth(1)))
	require.NoError(t, err)
	p, err := parser.New()
	require.NoError(t, err)
	node, err := p.Parse(t.Context(), `!(state == "active")`)
	require.NoError(t, err)
	_, err = e.Evaluate(node, map[string]any{"state": "active"})
	require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
}

func TestEnumAggregateCacheIsolation(t *testing.T) {
	t.Parallel()

	type state struct{ Name string }

	names := map[string]map[string]any{"state": {"ACTIVE": state{"active"}}}
	p, err := parser.New(parser.WithEnumNames(names))
	require.NoError(t, err)

	names["state"]["ACTIVE"] = state{"changed"}

	for range 8 {
		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			n, err := p.Parse(t.Context(), `state == "ACTIVE"`)
			require.NoError(t, err)

			lit := n.(*filter.BinaryOpNode).Right.(*filter.LiteralNode)
			require.Equal(t, state{"active"}, lit.Value)
			lit.Value = state{"caller change"}
		})
	}
}

func TestFieldPolicyCodecPrecedence(t *testing.T) {
	t.Parallel()

	for _, kind := range []filter.FieldKind{filter.FieldKindInt, filter.FieldKindDecimal} {
		for _, reject := range []bool{false, true} {
			var failure error
			if reject {
				failure = filter.ErrUnsupportedType
			}

			p, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"v": kind}), filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: literalValidationCodec{failure}}), filter.WithEnumValues(map[string][]int64{"v": {1}}))
			require.NoError(t, err)

			want := filter.ErrEnumValueNotAllowed
			if reject {
				want = failure
			}

			require.ErrorIs(t, p.Field("v").CheckLiteral(&filter.LiteralNode{Value: int64(2)}), want)
		}
	}
}

func TestCodecPreservesFiniteFloats(t *testing.T) {
	t.Parallel()

	p, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithPortableContract(true), filter.WithFieldTypes(map[string]filter.FieldKind{"v": filter.FieldKindFloat}), filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{filter.FieldKindFloat: literalValidationCodec{}}))
	require.NoError(t, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "v"}, Right: &filter.LiteralNode{Value: math.Inf(1)}}
	require.ErrorIs(t, filter.Validate(n, p), filter.ErrInvalidExpression)
}

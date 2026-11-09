// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func ident(name string) *filter.IdentNode { return &filter.IdentNode{Name: name} }

func lit(v any) *filter.LiteralNode { return &filter.LiteralNode{Value: v} }

func TestTranslate_ParsedExpressions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr  string
		where string
		args  []any
	}{
		{`!(a == 1)`, `("a" != ? OR "a" IS NULL)`, []any{int64(1)}},
		{`a != 1`, `("a" != ? OR "a" IS NULL)`, []any{int64(1)}},
		{`a < 1`, `"a" < ?`, []any{int64(1)}},
		{`a <= 1`, `"a" <= ?`, []any{int64(1)}},
		{`a > 1.5`, `"a" > ?`, []any{1.5}},
		{`a >= "x"`, `"a" >= ?`, []any{"x"}},
		{`a == null`, `"a" IS NULL`, nil},
		{`null != a`, `"a" IS NOT NULL`, nil},
		{`a in [1, 2]`, `"a" IN (?, ?)`, []any{int64(1), int64(2)}},
		{`a in []`, sqlbase.MatchNone, nil},
		{`a in [b, 1]`, `((COALESCE("a" = "b", "a" IS NULL AND "b" IS NULL)) OR ("a" = ?))`, []any{int64(1)}},
		{`a || b`, `("a" = <literal>) OR ("b" = <literal>)`, nil},
		{`size(a) > 2`, `length("a") > ?`, []any{int64(2)}},
		{`has(a.b)`, `"a"."b" IS NOT NULL`, nil},
		{`a.startsWith(b)`, `"a" ~ "b"`, nil},
		{`a.matches("^x")`, `"a" ~ ?`, []any{"^x"}},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			where, args, err := mustTranslator(t, failing{}).Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.where, where)

			if tc.args == nil {
				require.Empty(t, args)
			} else {
				require.Equal(t, tc.args, args)
			}
		})
	}
}

func TestTranslate_NilNodeMatchesAll(t *testing.T) {
	t.Parallel()

	where, args, err := mustTranslator(t, failing{}).Translate(nil)
	require.NoError(t, err)
	require.Equal(t, sqlbase.MatchAll, where)
	require.Empty(t, args)
}

func TestTranslateInline_NullAndValues(t *testing.T) {
	t.Parallel()

	where, err := mustTranslator(t, failing{}).TranslateInline(filtertest.MustParseFilter(t, `a == null || b in [1]`))
	require.NoError(t, err)
	require.Equal(t, `("a" IS NULL) OR ("b" IN (<literal>))`, where)
}

func TestTranslate_ParsedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		opts []filter.PolicyOption
		want error
	}{
		{`a < null`, nil, filter.ErrUnsupportedOperation},
		{`a.matches(b)`, nil, filter.ErrUnsupportedOperation},
		{`a.matches("abcdef")`, []filter.PolicyOption{filter.WithMaxRegexLength(3)}, nil},
		{`b == 1`, []filter.PolicyOption{filter.WithAllowedFields("a")}, filter.ErrFieldNotAllowed},
		{`a.contains(b)`, []filter.PolicyOption{filter.WithAllowedFields("a")}, filter.ErrFieldNotAllowed},
		{`a in [b]`, []filter.PolicyOption{filter.WithAllowedFields("a")}, filter.ErrFieldNotAllowed},
		{`!(b == 1)`, []filter.PolicyOption{filter.WithAllowedFields("a")}, filter.ErrFieldNotAllowed},
		{`a == 1 || (b == 1 && (c == 1 || d == 1))`, []filter.PolicyOption{filter.WithMaxDepth(1)}, filter.ErrMaxDepthExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := mustTranslator(t, failing{}, tc.opts...).Translate(filtertest.MustParseFilter(t, tc.expr))
			require.Error(t, err)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

func TestTranslate_HandBuiltNodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		node  filter.Node
		where string
		want  error
	}{
		{
			name:  "size with its operand as the single argument",
			node:  &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.CallNode{Op: filter.OpSize, Args: []filter.Node{ident("a")}}, Right: lit(int64(0))},
			where: `length("a") > ?`,
		},
		{
			name:  "exists",
			node:  &filter.CallNode{Op: filter.OpExists, Target: ident("a")},
			where: `"a" IS NOT NULL`,
		},
		{
			name: "size outside a comparison",
			node: &filter.CallNode{Op: filter.OpSize, Target: ident("a")},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "unknown function",
			node: &filter.CallNode{Op: filter.OpSubstring, Target: ident("a")},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "unknown unary operator",
			node: &filter.UnaryOpNode{Op: filter.OpEqual, Operand: ident("a")},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "unknown comparison operator",
			node: &filter.BinaryOpNode{Op: filter.OpContains, Left: ident("a"), Right: lit("x")},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "in without a list",
			node: &filter.BinaryOpNode{Op: filter.OpIn, Left: ident("a"), Right: lit("x")},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "in with an unrenderable element",
			node: &filter.BinaryOpNode{Op: filter.OpIn, Left: ident("a"), Right: &filter.ListNode{Elements: []filter.Node{lit(struct{}{})}}},
			want: filter.ErrUnsupportedType,
		},
		{
			name: "in with an unrenderable left operand",
			node: &filter.BinaryOpNode{Op: filter.OpIn, Left: lit(struct{}{}), Right: &filter.ListNode{Elements: []filter.Node{lit(int64(1))}}},
			want: filter.ErrUnsupportedType,
		},
		{
			name: "literal as a predicate",
			node: lit(true),
			want: filter.ErrInvalidExpression,
		},
		{
			name: "non-clause visitor result",
			node: &filter.ListNode{Elements: []filter.Node{lit(int64(1))}},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "function call as a comparison operand",
			node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpContains, Target: ident("a"), Args: []filter.Node{lit("x")}}, Right: lit(true)},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "string function on a literal target",
			node: &filter.CallNode{Op: filter.OpContains, Target: lit("a"), Args: []filter.Node{lit("x")}},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "string function without arguments",
			node: &filter.CallNode{Op: filter.OpEndsWith, Target: ident("a")},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "string function with a non-string argument",
			node: &filter.CallNode{Op: filter.OpContains, Target: ident("a"), Args: []filter.Node{lit(int64(1))}},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "string function with an unsupported literal",
			node: &filter.CallNode{Op: filter.OpContains, Target: ident("a"), Args: []filter.Node{lit(struct{}{})}},
			want: filter.ErrUnsupportedType,
		},
		{
			name: "exists over a literal",
			node: &filter.CallNode{Op: filter.OpHas, Target: lit("a")},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "size over a literal",
			node: &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.CallNode{Op: filter.OpSize, Target: lit("a")}, Right: lit(int64(0))},
			want: filter.ErrInvalidExpression,
		},
		{
			name: "null comparison over an unrenderable operand",
			node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpContains, Target: ident("a")}, Right: lit(nil)},
			want: filter.ErrUnsupportedOperation,
		},
		{
			name: "right operand failure",
			node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("a"), Right: lit(struct{}{})},
			want: filter.ErrUnsupportedType,
		},
		{
			name: "logical right operand failure",
			node: &filter.BinaryOpNode{Op: filter.OpAnd, Left: ident("a"), Right: lit("x")},
			want: filter.ErrInvalidExpression,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			where, _, err := mustTranslator(t, failing{}).Translate(tc.node)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.where, where)
		})
	}
}

func TestTranslate_InWithEmptyListRollsBackArgs(t *testing.T) {
	t.Parallel()

	// The left operand of `x in []` is still rendered (so the allow-list
	// runs) but its placeholder is dropped with the constant result.
	node := &filter.BinaryOpNode{Op: filter.OpAnd,
		Left:  &filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("a"), Right: lit(int64(1))},
		Right: &filter.BinaryOpNode{Op: filter.OpIn, Left: &filter.CallNode{Op: filter.OpSize, Target: ident("b")}, Right: &filter.ListNode{}},
	}
	where, args, err := mustTranslator(t, failing{}).Translate(node)
	require.NoError(t, err)
	require.Equal(t, `("a" = ?) AND (`+sqlbase.MatchNone+`)`, where)
	require.Equal(t, []any{int64(1)}, args)
}

func TestTranslate_RegexLengthDefault(t *testing.T) {
	t.Parallel()

	node := &filter.CallNode{Op: filter.OpMatches, Target: ident("a"), Args: []filter.Node{lit(strings.Repeat("a", filter.DefaultMaxRegexLength+1))}}
	_, _, err := mustTranslator(t, failing{}).Translate(node)
	require.Error(t, err)
}

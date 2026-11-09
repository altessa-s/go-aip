// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// exprCase is one evaluation of an expression form of the MongoDB translator: the outcomes below are those of the translator's
// $expr query on MongoDB 8 for the same documents.
type exprCase struct {
	expr string
	row  map[string]any
	want bool
}

func mongoExprEvaluator(t *testing.T, schema ...mongo.SchemaOption) *evaluator.Evaluator {
	t.Helper()

	s, err := mongo.NewSchema(schema...)
	require.NoError(t, err)

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags")), evaluator.WithProfile(s))
	require.NoError(t, err)

	return eval
}

func runExprCases(t *testing.T, eval *evaluator.Evaluator, cases []exprCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tc.expr)
			got, err := eval.Evaluate(node, tc.row)
			require.NoError(t, err)
			require.Equal(t, tc.want, got, "%s on %v", tc.expr, tc.row)

			prepared, err := eval.Prepare(node)
			require.NoError(t, err)

			got, err = prepared.Evaluate(tc.row)
			require.NoError(t, err)
			require.Equal(t, tc.want, got, "prepared %s on %v", tc.expr, tc.row)
		})
	}
}

func TestMongoExprFieldOperands(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	runExprCases(t, mongoExprEvaluator(t), []exprCase{
		// Missing and null are the same null; two nulls are equal; != is the negation of ==.
		{"a == b", map[string]any{}, true},
		{"a == b", map[string]any{"a": nil}, true},
		{"a == b", map[string]any{"a": int64(1)}, false},
		{"a != b", map[string]any{}, false},
		{"a != b", map[string]any{"b": int64(1)}, true},
		{"b == a", map[string]any{"a": nil, "b": nil}, true},
		// Whole values: an array equals only an equal array, numbers compare across types.
		{"a == b", map[string]any{"a": []any{int64(1), int64(2)}, "b": int64(1)}, false},
		{"a == b", map[string]any{"a": []any{int64(1), int64(2)}, "b": []any{int64(1), int64(2)}}, true},
		{"a == b", map[string]any{"a": []int{1, 2}, "b": []any{int64(1), int64(2)}}, true},
		{"a == b", map[string]any{"a": int64(1), "b": 1.0}, true},
		{"a == b", map[string]any{"a": decimal.RequireFromString("2"), "b": int64(2)}, true},
		{"a == b", map[string]any{"a": decimal.RequireFromString("0.1"), "b": 0.1}, false},
		{"a == b", map[string]any{"a": int64(1), "b": "1"}, false},
		{"a == b", map[string]any{"a": map[string]any{"x": int64(1)}, "b": map[string]any{"x": int64(1)}}, true},
		{"a == b", map[string]any{"a": map[string]any{"x": int64(1)}, "b": map[string]any{"x": int64(1), "y": int64(0)}}, false},
		{"a == b", map[string]any{"a": []byte("ab"), "b": []byte("ab")}, true},
		{"a == b", map[string]any{"a": t0.Add(500 * time.Microsecond), "b": t0}, true},
		{"a == b", map[string]any{"a": math.NaN(), "b": math.NaN()}, true},
		{"a == b", map[string]any{"a": true, "b": true}, true},
		// Ordering holds only between non-null values of one type class.
		{"a < b", map[string]any{"a": int64(1), "b": int64(2)}, true},
		{"a < b", map[string]any{"a": int64(1), "b": "2"}, false},
		{"a > b", map[string]any{"a": int64(1), "b": "2"}, false},
		{"a < b", map[string]any{"b": int64(1)}, false},
		{"a <= b", map[string]any{}, false},
		{"a < b", map[string]any{"a": "abc", "b": "b"}, true},
		{"a >= b", map[string]any{"a": decimal.RequireFromString("2.5"), "b": int64(2)}, true},
		{"a < b", map[string]any{"a": math.NaN(), "b": int64(1)}, true},
		{"a > b", map[string]any{"a": math.Inf(1), "b": int64(1)}, true},
		{"a < b", map[string]any{"a": false, "b": true}, true},
		{"a < b", map[string]any{"a": t0, "b": t0.Add(time.Second)}, true},
		{"a < b", map[string]any{"a": []byte("b"), "b": []byte("ab")}, true},
		{"a < b", map[string]any{"a": []any{int64(1), int64(2)}, "b": []any{int64(1), int64(3)}}, true},
		{"a < b", map[string]any{"a": []any{int64(1)}, "b": []any{int64(1), int64(0)}}, true},
		{"a < b", map[string]any{"a": []any{int64(1), "x"}, "b": []any{int64(1), int64(5)}}, false},
		{"a < b", map[string]any{"a": map[string]any{"x": int64(1)}, "b": map[string]any{"x": int64(2)}}, true},
		{"a < b", map[string]any{"a": map[string]any{"x": int64(1)}, "b": map[string]any{"y": int64(0)}}, true},
		{"a > b", map[string]any{"a": map[string]any{"x": int64(1), "y": int64(0)}, "b": map[string]any{"x": int64(1)}}, true},
		// A path through a list of documents reads the list of the values found.
		{"s.p == a", map[string]any{"s": []any{map[string]any{"p": int64(1)}, map[string]any{}}, "a": []any{int64(1)}}, true},
		{"s.p == a", map[string]any{"s": []any{map[string]any{"p": int64(1)}, map[string]any{"p": int64(2)}}, "a": int64(1)}, false},
		{"s.p < a", map[string]any{"s": map[string]any{"p": int64(1)}, "a": int64(2)}, true},
		// An array field compared with an operand that is not an array also matches when it holds it.
		{"a in tags", map[string]any{"a": "x", "tags": []any{"x", "y"}}, true},
		{"a in tags", map[string]any{"a": "z", "tags": []any{"x", "y"}}, false},
		{"a in tags", map[string]any{"a": "x", "tags": "x"}, true},
		{"a in tags", map[string]any{"tags": []any{nil}}, true},
		{"a in tags", map[string]any{"tags": []any{}}, false},
		{"tags == a", map[string]any{"a": "x", "tags": []string{"x"}}, true},
		{"tags != a", map[string]any{"a": "x", "tags": []any{"x", "y"}}, false},
		{"!(a in tags)", map[string]any{"a": "z", "tags": []any{"x", "y"}}, true},
		{"a == b || a == 1", map[string]any{"a": int64(1), "b": int64(2)}, true},
		{"!(a == b)", map[string]any{"a": int64(1), "b": int64(2)}, true},
	})
}

func TestMongoExprStringFunctions(t *testing.T) {
	t.Parallel()

	runExprCases(t, mongoExprEvaluator(t), []exprCase{
		{"a.contains(b)", map[string]any{"a": "abc", "b": "b"}, true},
		{"a.contains(b)", map[string]any{"a": "abc", "b": ""}, true},
		{"a.contains(b)", map[string]any{"a": "Привет", "b": "ив"}, true},
		{"a.contains(b)", map[string]any{"a": "abc", "b": "abcd"}, false},
		{"a.contains(b)", map[string]any{"a": "abc"}, false},
		{"a.contains(b)", map[string]any{"a": int64(1), "b": "1"}, false},
		{"a.contains(b)", map[string]any{"a": []any{"abc"}, "b": "b"}, false},
		{"a.startsWith(b)", map[string]any{"a": "abc", "b": "ab"}, true},
		{"a.startsWith(b)", map[string]any{"a": "abc", "b": "bc"}, false},
		{"a.endsWith(b)", map[string]any{"a": "abc", "b": "bc"}, true},
		{"a.endsWith(b)", map[string]any{"a": "b", "b": "abc"}, false},
		{"!a.contains(b)", map[string]any{"a": int64(1), "b": "x"}, true},
		{"!a.startsWith(b)", map[string]any{"a": "abc", "b": "ab"}, false},
	})
}

func TestMongoExprSubstring(t *testing.T) {
	t.Parallel()

	runExprCases(t, mongoExprEvaluator(t), []exprCase{
		{`a.substring(1) == "bc"`, map[string]any{"a": "abc"}, true},
		{`a.substring(0, 1) == "a"`, map[string]any{"a": "abc"}, true},
		{`a.substring(1, 3) == "ри"`, map[string]any{"a": "Привет"}, true},
		{`a.substring(3) == ""`, map[string]any{"a": "abc"}, true},
		{`a.substring(0, 3) == "abc"`, map[string]any{"a": "abc"}, true},
		{`a.substring(0, 0) == ""`, map[string]any{"a": ""}, true},
		// Out of range, or a target that is not a string: no value, so every comparison is false, != included.
		{`a.substring(4) == ""`, map[string]any{"a": "abc"}, false},
		{`a.substring(0, 9) == "abc"`, map[string]any{"a": "abc"}, false},
		{`a.substring(0, 9) != "abc"`, map[string]any{"a": "abc"}, false},
		{`a.substring(0) != "x"`, map[string]any{"a": int64(1)}, false},
		{`a.substring(0) != "x"`, map[string]any{}, false},
		{`a.substring(0, 1) < "b"`, map[string]any{"a": "abc"}, true},
		{`"bc" == a.substring(1)`, map[string]any{"a": "abc"}, true},
		{`a.substring(1) == b`, map[string]any{"a": "abc", "b": "bc"}, true},
		{`a.substring(0, 1) == b.substring(0, 1)`, map[string]any{"a": "abc", "b": "ab"}, true},
		{`a.substring(0, 1) in ["x", "a"]`, map[string]any{"a": "abc"}, true},
		{`a.substring(0, 1) in [b]`, map[string]any{"a": "abc", "b": "a"}, true},
		// The translator renders this one as {$or: null}, which MongoDB rejects; the evaluator answers false, as for any empty list.
		{`a.substring(0, 1) in []`, map[string]any{"a": "abc"}, false},
		{`a.substring(0, 1) in tags`, map[string]any{"a": "abc", "tags": []any{"a"}}, true},
	})
}

func TestMongoExprSizeAndIn(t *testing.T) {
	t.Parallel()

	runExprCases(t, mongoExprEvaluator(t), []exprCase{
		// A missing or null value counts as an empty array; a value without a size has no value.
		{"a.size() == b", map[string]any{"a": []any{int64(1), int64(2)}, "b": int64(2)}, true},
		{"a.size() == b", map[string]any{"b": int64(0)}, true},
		{"a.size() == b", map[string]any{"a": "abc", "b": int64(3)}, true},
		{"a.size() != b", map[string]any{"a": int64(5), "b": int64(0)}, false},
		{"a.size() < b", map[string]any{"a": []any{int64(1)}, "b": int64(2)}, true},
		{"a == b.size()", map[string]any{"a": int64(0)}, true},
		{"a.size() == b.size()", map[string]any{"a": "ab", "b": []any{int64(1), int64(2)}}, true},
		{"a.size() in [b]", map[string]any{"a": []any{int64(1)}, "b": int64(1)}, true},
		// A list holding a field: a query in of the literals or an expression equality per other element.
		{"a in [b]", map[string]any{}, true},
		{"a in [b]", map[string]any{"a": int64(2), "b": int64(2)}, true},
		{"a in [b, 1]", map[string]any{"a": int64(1), "b": int64(2)}, true},
		{"a in [b, 1]", map[string]any{"a": []any{int64(1), int64(3)}, "b": int64(2)}, true},
		{"a in [b, 1]", map[string]any{"a": int64(3), "b": int64(2)}, false},
		{`a in [b, "x"]`, map[string]any{"a": "x"}, true},
		{"a in [1, b, null]", map[string]any{"a": int64(5), "b": int64(6)}, false},
		{"!(a in [b, 1])", map[string]any{"a": int64(3), "b": int64(2)}, true},
	})
}

func TestMongoExprSchemaRules(t *testing.T) {
	t.Parallel()

	t.Run("presence", func(t *testing.T) {
		t.Parallel()

		runExprCases(t, mongoExprEvaluator(t, mongo.WithSchemaPresenceFields("a", "b")), []exprCase{
			// A null presence field makes every comparison false, != included.
			{"a == b", map[string]any{}, false},
			{"a != b", map[string]any{"b": int64(1)}, false},
			{"!(a == b)", map[string]any{"b": int64(1)}, false},
			{"a != b", map[string]any{"a": int64(1), "b": int64(2)}, true},
			{"a in [b, 1]", map[string]any{"b": int64(1)}, false},
			{"!(a in [b, 2])", map[string]any{"b": int64(1)}, false},
			{"!(a in [b, 2])", map[string]any{"a": int64(1), "b": int64(3)}, true},
			// A negated string function reading a presence field holds only on strings.
			{"!a.contains(b)", map[string]any{"a": int64(1), "b": "x"}, false},
			{"!a.contains(b)", map[string]any{"a": "abc", "b": int64(1)}, false},
			{"!a.contains(b)", map[string]any{"a": "abc", "b": "x"}, true},
			{"!c.contains(a)", map[string]any{"c": "abc", "a": "x"}, true},
			{"!c.contains(a)", map[string]any{"c": int64(1), "a": "x"}, false},
			{`!a.contains("b")`, map[string]any{"a": int64(5)}, false},
			{`!a.contains("b")`, map[string]any{"a": "xyz"}, true},
			{`!a.contains("b")`, map[string]any{"a": []any{"xyz", int64(1)}}, true},
		})
	})

	t.Run("zero when absent", func(t *testing.T) {
		t.Parallel()

		runExprCases(t, mongoExprEvaluator(t, mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"b": filter.FieldKindInt, "c": filter.FieldKindString})), []exprCase{
			{"a == b", map[string]any{"a": int64(0)}, true},
			{"a == b", map[string]any{"a": int64(0), "b": nil}, true},
			{"a != b", map[string]any{}, true},
			{"a < b", map[string]any{"a": int64(-1)}, true},
			{"a in [b]", map[string]any{"a": int64(0)}, true},
			{"a.contains(c)", map[string]any{"a": "abc"}, true},
			{`c.substring(0) == ""`, map[string]any{}, true},
		})
	})

	t.Run("omit empty", func(t *testing.T) {
		t.Parallel()

		runExprCases(t, mongoExprEvaluator(t, mongo.WithSchemaOmitEmpty("b")), []exprCase{
			{"a == b", map[string]any{"a": "", "b": ""}, false},
			{"a == b", map[string]any{"a": nil, "b": ""}, true},
			{"a == b", map[string]any{"a": "x", "b": "x"}, true},
			{"a.contains(b)", map[string]any{"a": "", "b": ""}, false},
			{"b.size() == a", map[string]any{"a": int64(0), "b": ""}, true},
		})
	})
}

func TestMongoExprValidation(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t,
		filter.WithAllowedFields("a", "b", "tags", "n", "name", "price"),
		filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"n": filter.FieldKindInt, "name": filter.FieldKindString, "price": filter.FieldKindDecimal}))

	schema, err := mongo.NewSchema(mongo.WithSchemaPresenceFields("a"))
	require.NoError(t, err)

	eval, err := evaluator.New(policy, evaluator.WithProfile(schema))
	require.NoError(t, err)

	tests := []struct {
		expr string
		want error
	}{
		{`a == hidden`, filter.ErrFieldNotAllowed},
		{`hidden.contains(a)`, filter.ErrFieldNotAllowed},
		{`a.contains(hidden)`, filter.ErrFieldNotAllowed},
		{`hidden.substring(0) == "x"`, filter.ErrFieldNotAllowed},
		{`a.size() == hidden`, filter.ErrFieldNotAllowed},
		{`a.substring(-1) == "x"`, filter.ErrInvalidExpression},
		{`a.substring(2, 1) == "x"`, filter.ErrInvalidExpression},
		{`a.substring(1.5) == "x"`, filter.ErrInvalidExpression},
		{`a.substring(2147483648) == "x"`, filter.ErrInvalidExpression},
		{`a.substring(b) == "x"`, filter.ErrUnsupportedOperation},
		{`a.substring(0, 1, 2) == "x"`, filter.ErrInvalidExpression},
		{`a.substring() == "x"`, filter.ErrInvalidExpression},
		{`a.substring(0) == null`, filter.ErrUnsupportedOperation},
		{`name.substring(0) == 1`, filter.ErrFieldTypeMismatch},
		{`n.substring(0) == "x"`, filter.ErrFieldTypeMismatch},
		{`tags.substring(0) == "x"`, filter.ErrUnsupportedOperation},
		{`a.size().substring(0) == "x"`, filter.ErrUnsupportedOperation},
		{`a.substring(0).substring(0) == "x"`, filter.ErrUnsupportedOperation},
		{`n == name`, filter.ErrFieldTypeMismatch},
		{`n < tags`, filter.ErrUnsupportedOperation},
		{`tags < n`, filter.ErrUnsupportedOperation},
		{`tags.contains(a)`, filter.ErrUnsupportedOperation},
		{`a.contains(tags)`, filter.ErrUnsupportedOperation},
		{`a.contains(b.size())`, filter.ErrUnsupportedOperation},
		{`a.size().contains(b)`, filter.ErrUnsupportedOperation},
		{`a.size() == -1`, filter.ErrInvalidExpression},
		{`n in [name]`, filter.ErrFieldTypeMismatch},
		{`a in [b, tags.size().size()]`, filter.ErrUnsupportedOperation},
		{`a.size() in [b, 1]`, filter.ErrUnsupportedOperation},
		{`a.substring(0).size() == 1`, filter.ErrUnsupportedOperation},
		{`a.matches(b)`, filter.ErrUnsupportedOperation},
		{`!(a in tags)`, filter.ErrInvalidExpression},
		{`!(a in tags) && n == 1`, filter.ErrInvalidExpression},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tt.expr)
			require.ErrorIs(t, eval.Validate(node), tt.want)

			_, err := eval.Evaluate(node, map[string]any{})
			require.ErrorIs(t, err, tt.want)
		})
	}

	for _, expr := range []string{`a == b`, `a in [b, 1]`, `a.contains(b)`, `a.substring(0, 1) == "x"`, `price == n`, `price in [a, 1.5]`, `!(!(a in tags))`, `a.size() in [b]`, `name.substring(0) in tags`} {
		require.NoError(t, eval.Validate(filtertest.MustParseFilter(t, expr)), expr)
	}

	for _, node := range []filter.Node{
		&filter.BinaryOpNode{Op: filter.OpAnd, Left: &filter.IdentNode{Name: "a"}},
		&filter.UnaryOpNode{Op: filter.OpNot},
		&filter.BinaryOpNode{Op: filter.OpOr, Left: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "a"}, Right: &filter.IdentNode{Name: "b"}}},
	} {
		require.ErrorIs(t, eval.Validate(node), filter.ErrInvalidExpression)
	}

	substring := &filter.CallNode{Op: filter.OpSubstring, Target: &filter.IdentNode{Name: "a"}, Args: []filter.Node{&filter.LiteralNode{Value: int64(0)}}}
	require.ErrorIs(t, eval.Validate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: substring, Right: &filter.LiteralNode{Value: map[string]any{}}}), filter.ErrUnsupportedType)
	require.ErrorIs(t, eval.Validate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpSize}, Right: &filter.IdentNode{Name: "a"}}), filter.ErrInvalidExpression)
	require.ErrorIs(t, eval.Validate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: substring, Right: &filter.ListNode{}}), filter.ErrUnsupportedOperation)
	require.ErrorIs(t, eval.Validate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: substring, Right: &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "a"}}}), filter.ErrUnsupportedOperation)
	require.ErrorIs(t, eval.Validate(&filter.BinaryOpNode{Op: filter.OpSubstring, Left: &filter.IdentNode{Name: "a"}, Right: &filter.IdentNode{Name: "b"}}), filter.ErrUnsupportedOperation)
}

func TestMongoExprDataBudget(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema()
	require.NoError(t, err)

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags")),
		evaluator.WithProfile(schema), evaluator.WithMaxDataBudget(64))
	require.NoError(t, err)

	long := strings.Repeat("x", 4096)
	many := make([]any, 512)

	for i := range many {
		many[i] = int64(i)
	}

	for _, tc := range []struct {
		expr string
		row  map[string]any
	}{
		{"a.contains(b)", map[string]any{"a": long, "b": "y"}},
		{"a.substring(1) == b", map[string]any{"a": long, "b": "y"}},
		{"a.size() == b", map[string]any{"a": long, "b": int64(1)}},
		{"a == b", map[string]any{"a": many, "b": many}},
		{"a in tags", map[string]any{"a": int64(-1), "tags": many}},
		{"a == b", map[string]any{"a": map[string]any{"k": many}, "b": map[string]any{"k": many}}},
		{"a in [b, 1]", map[string]any{"a": many, "b": many}},
	} {
		_, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.row)
		require.ErrorIs(t, err, filter.ErrDataLimitExceeded, tc.expr)
	}
}

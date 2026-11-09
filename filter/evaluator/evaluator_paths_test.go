// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// pathCase evaluates expr on row and expects want, or an error matching err.
type pathCase struct {
	expr string
	row  map[string]any
	want bool
	err  error
}

func runPathCases(t *testing.T, eval *evaluator.Evaluator, cases []pathCase) {
	t.Helper()

	for _, tc := range cases {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.row)
		if tc.err != nil {
			require.ErrorIs(t, err, tc.err, "%s on %v", tc.expr, tc.row)
			continue
		}

		require.NoError(t, err, "%s on %v", tc.expr, tc.row)
		require.Equal(t, tc.want, got, "%s on %v", tc.expr, tc.row)
	}
}

// TestDefaultEvaluatorPaths covers the default evaluator's comparison, membership, string, size and presence paths.
func TestDefaultEvaluatorPaths(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags", "steps.codes")))
	require.NoError(t, err)

	type label string

	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	runPathCases(t, eval, []pathCase{
		// Numbers compare exactly across the integer and float kinds.
		{expr: "n == 1", row: map[string]any{"n": int32(1)}, want: true},
		{expr: "n == 1", row: map[string]any{"n": 1}, want: true},
		{expr: "n < 2", row: map[string]any{"n": uint64(1)}, want: true},
		{expr: "n > 1u", row: map[string]any{"n": int64(-1)}, want: false},
		{expr: "n < 1u", row: map[string]any{"n": int64(-1)}, want: true},
		{expr: "n == 9223372036854775807", row: map[string]any{"n": uint64(math.MaxInt64)}, want: true},
		{expr: "n > 1.5", row: map[string]any{"n": int64(2)}, want: true},
		{expr: "n < 1.5", row: map[string]any{"n": uint64(1)}, want: true},
		{expr: "n == 1.0", row: map[string]any{"n": math.NaN()}, want: false},
		{expr: "n != 1", row: map[string]any{"n": math.NaN()}, want: true},
		{expr: "n == 1.0", row: map[string]any{"n": math.Inf(1)}, want: false},
		{expr: "n > 1", row: map[string]any{"n": math.Inf(1)}, want: true},
		{expr: "n < 1", row: map[string]any{"n": math.Inf(-1)}, want: true},
		{expr: "n > 1u", row: map[string]any{"n": -0.5}, want: false},
		{expr: "n < 18446744073709551615u", row: map[string]any{"n": 1.8446744073709552e19}, want: false},
		{expr: "n == 1", row: map[string]any{"n": decimal.RequireFromString("1.0")}, want: true},
		{expr: "n == 0.3", row: map[string]any{"n": decimal.RequireFromString("0.3")}, want: true},
		// Strings, bools, timestamps, bytes.
		{expr: `s == "x"`, row: map[string]any{"s": label("x")}, want: true},
		{expr: `s < "b"`, row: map[string]any{"s": "a"}, want: true},
		{expr: "b == true", row: map[string]any{"b": true}, want: true},
		{expr: "b != false", row: map[string]any{"b": true}, want: true},
		{expr: "b < true", row: map[string]any{"b": false}, err: filter.ErrUnsupportedOperation},
		{expr: `t > timestamp("2025-01-01T00:00:00Z")`, row: map[string]any{"t": when}, want: true},
		{expr: `d == b"ab"`, row: map[string]any{"d": []byte("ab")}, want: true},
		{expr: `d < b"b"`, row: map[string]any{"d": []byte("ab")}, want: true},
		{expr: `s == 1`, row: map[string]any{"s": "x"}, err: filter.ErrUnsupportedType},
		{expr: `s == null`, row: map[string]any{}, want: true},
		{expr: `s != null`, row: map[string]any{"s": "x"}, want: true},
		// Field operands.
		{expr: "a == b", row: map[string]any{"a": int64(1), "b": 1.0}, want: true},
		{expr: "a < b", row: map[string]any{"a": "a", "b": "b"}, want: true},
		// Membership in a list literal, in a data list and in an array field.
		{expr: "n in [1, 2]", row: map[string]any{"n": int32(2)}, want: true},
		{expr: `n in ["1", 2.5]`, row: map[string]any{"n": int64(1)}, want: false},
		{expr: "n in [null]", row: map[string]any{}, want: true},
		{expr: `t in [timestamp("2026-01-01T00:00:00Z")]`, row: map[string]any{"t": when}, want: true},
		{expr: `d in [b"ab"]`, row: map[string]any{"d": []byte("ab")}, want: true},
		{expr: "b in [true]", row: map[string]any{"b": true}, want: true},
		{expr: "n in [1.5]", row: map[string]any{"n": decimal.RequireFromString("1.5")}, want: true},
		{expr: `"x" in list`, row: map[string]any{"list": []any{"y", "x"}}, want: true},
		{expr: `"x" in list`, row: map[string]any{"list": []any{"y"}}, want: false},
		{expr: `v in list`, row: map[string]any{"v": label("x"), "list": []any{label("y"), label("x")}}, want: true},
		{expr: `v in list`, row: map[string]any{"v": [2]int{1, 2}, "list": []any{[2]int{1, 2}}}, want: true},
		{expr: `v in list`, row: map[string]any{"v": []int{1}, "list": []any{[]int{1}}}, want: false},
		{expr: `"x" in list`, row: map[string]any{"list": "x"}, err: filter.ErrInvalidExpression},
		{expr: `"red" in tags`, row: map[string]any{"tags": []string{"red"}}, want: true},
		{expr: `tags == "red"`, row: map[string]any{"tags": []any{"blue", "red"}}, want: true},
		{expr: `tags != "red"`, row: map[string]any{"tags": []any{"blue"}}, want: true},
		{expr: `tags > "a"`, row: map[string]any{"tags": []any{nil, "b"}}, want: true},
		{expr: `tags in ["red"]`, row: map[string]any{"tags": []any{"red"}}, want: true},
		{expr: `tags in list`, row: map[string]any{"tags": []any{"red"}, "list": []any{"red"}}, want: true},
		{expr: `steps.codes == "a"`, row: map[string]any{"steps": []any{map[string]any{"codes": []any{"a"}}, "x"}}, want: true},
		// String functions over strings, array elements and other values.
		{expr: `s.contains("b")`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.startsWith("a")`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.endsWith("c")`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.contains("b")`, row: map[string]any{"s": int64(1)}, want: false},
		{expr: `tags.contains("e")`, row: map[string]any{"tags": []any{int64(1), "red"}}, want: true},
		{expr: `s.matches("^a.c$")`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.matches("^a")`, row: map[string]any{"s": int64(1)}, want: false},
		{expr: `tags.matches("^r")`, row: map[string]any{"tags": []any{"blue", "red"}}, want: true},
		{expr: `s.substring(1, 2) == "b"`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.substring(1) == "bc"`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s.substring(4) == ""`, row: map[string]any{"s": "abc"}, want: false}, // out of range: unknown, as in SQL
		{expr: `!(s.substring(4) == "")`, row: map[string]any{"s": "abc"}, want: false},
		{expr: `s.substring(0) == ""`, row: map[string]any{"s": int64(1)}, err: filter.ErrInvalidExpression},
		{expr: `s.substring(n) == "c"`, row: map[string]any{"s": "abc", "n": int64(2)}, want: true},
		{expr: `s.substring(n) == "c"`, row: map[string]any{"s": "abc", "n": "x"}, err: filter.ErrInvalidExpression},
		// size() and has().
		{expr: "s.size() == 3", row: map[string]any{"s": "abc"}, want: true},
		{expr: "s.size() == 2", row: map[string]any{"s": "Пр"}, want: true},
		{expr: "list.size() == 2", row: map[string]any{"list": []any{1, 2}}, want: true},
		{expr: "tags.size() == 0", row: map[string]any{}, want: true},
		{expr: "n.size() == 0", row: map[string]any{"n": int64(1)}, err: filter.ErrUnsupportedOperation},
		{expr: "n.size() == 0", row: map[string]any{}, want: false}, // size() of an absent value is unknown
		{expr: "!(n.size() == 0)", row: map[string]any{}, want: false},
		{expr: "has(m.k)", row: map[string]any{"m": map[string]any{"k": nil}}, want: true},
		{expr: "has(m.k)", row: map[string]any{"m": map[string]any{}}, want: false},
		// Logical operators.
		{expr: "b && n == 1", row: map[string]any{"b": true, "n": int64(1)}, want: true},
		{expr: "b || n == 1", row: map[string]any{"b": false, "n": int64(2)}, want: false},
		{expr: "!b", row: map[string]any{"b": false}, want: true},
		{expr: "b && n == 1", row: map[string]any{"b": "x"}, err: filter.ErrInvalidExpression},
		{expr: "!s", row: map[string]any{"s": "x"}, err: filter.ErrInvalidExpression},
		{expr: "s", row: map[string]any{"s": "x"}, err: filter.ErrInvalidExpression},
	})
}

func TestLookup(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("a", "b.c", "tags"), filter.WithArrayFields("tags")),
		evaluator.WithMaxDataBudget(16))
	require.NoError(t, err)

	value, present, err := eval.Lookup(map[string]any{"b": map[string]any{"c": int64(2)}}, "b.c")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, int64(2), value)

	value, present, err = eval.Lookup(map[string]any{"tags": []any{"x"}}, "tags")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, []any{"x"}, value)

	_, present, err = eval.Lookup(map[string]any{}, "a")
	require.NoError(t, err)
	require.False(t, present)

	_, _, err = eval.Lookup(map[string]any{}, "hidden")
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)

	_, _, err = eval.Lookup(map[string]any{"tags": make([]string, 1024)}, "tags")
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

// TestRegexCacheStats: matches() patterns are compiled once per policy and counted.
func TestRegexCacheStats(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	require.Zero(t, eval.RegexCacheStatsSnapshot().HitRate())

	node := filtertest.MustParseFilter(t, `s.matches("^ab+$")`)
	for range 3 {
		_, err := eval.Evaluate(node, map[string]any{"s": "abb"})
		require.NoError(t, err)
	}

	stats := eval.RegexCacheStatsSnapshot()
	require.Positive(t, stats.TotalLookups())
	require.Positive(t, stats.Hits)
	require.Positive(t, stats.HitRate())

	eval.ResetRegexCacheStats()
	require.Zero(t, eval.RegexCacheStatsSnapshot().TotalLookups())
}

// errBoom is the error of the caseless comparator.
var errBoom = errors.New("boom")

// caseless is a Comparator that compares strings case-insensitively and delegates everything else.
type caseless struct{}

func (caseless) Compare(op filter.Operator, a, b any) (matched, handled bool, err error) {
	x, xok := a.(string)
	y, yok := b.(string)

	switch {
	case x == "boom" || y == "boom":
		return false, true, errBoom
	case !xok || !yok || (op != filter.OpEqual && op != filter.OpNotEqual):
		return false, false, nil
	default:
		return strings.EqualFold(x, y) == (op == filter.OpEqual), true, nil
	}
}

func TestWithComparator(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags")), evaluator.WithComparator(caseless{}))
	require.NoError(t, err)

	runPathCases(t, eval, []pathCase{
		{expr: `s == "ABC"`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `s != "ABC"`, row: map[string]any{"s": "abc"}, want: false},
		{expr: `s in ["X", "ABC"]`, row: map[string]any{"s": "abc"}, want: true},
		{expr: `"RED" in tags`, row: map[string]any{"tags": []any{"red"}}, want: true},
		{expr: `s < "b"`, row: map[string]any{"s": "a"}, want: true},
		{expr: `n == 1`, row: map[string]any{"n": int64(1)}, want: true},
		{expr: `s == "x"`, row: map[string]any{"s": "boom"}, err: errBoom},
	})
}

// symbol, binary and token are types of a test storage encoding: a symbol compares as a string, a binary has a subtype, a token
// is opaque and equal by value.
type (
	symbol string
	binary struct {
		sub  byte
		data []byte
	}
	token struct{ id int }
	// record is a struct the encoding stores as a document.
	record struct{ Name string }
	// elem is a document element type.
	elem struct {
		Key   string
		Value any
	}
)

// encoding is a test ValueEncoding with every optional capability.
type encoding struct{}

func (encoding) Stored(v any, b evaluator.Budget) (any, bool) {
	switch x := v.(type) {
	case symbol, binary, token:
		return v, true
	case []elem:
		if !b.Charge(len(x)) {
			return v, true
		}

		doc := make(map[string]any, len(x))
		for _, e := range x {
			doc[e.Key] = e.Value
		}

		return doc, true
	default:
		return nil, false
	}
}

func (encoding) Encode(v any, _ evaluator.Budget) any {
	switch x := v.(type) {
	case record:
		return map[string]any{"name": x.Name}
	case *record:
		return map[string]any{"name": x.Name}
	default:
		return v
	}
}

func (encoding) EncodesItself(reflect.Type) bool { return false }

func (encoding) Owns(t reflect.Type) bool {
	return t == reflect.TypeFor[symbol]() || t == reflect.TypeFor[binary]() || t == reflect.TypeFor[token]()
}

func (encoding) DocumentElement(t reflect.Type) bool { return t == reflect.TypeFor[elem]() }

func (encoding) StringValue(v any) (string, bool) {
	s, ok := v.(symbol)
	return string(s), ok
}

func (encoding) BinaryValue(v any) (byte, []byte, bool) {
	b, ok := v.(binary)
	return b.sub, b.data, ok
}

func (encoding) Equal(a, b any) bool {
	x, xok := a.(token)
	y, yok := b.(token)

	return xok && yok && x == y
}

func (encoding) ScanLength(v any) (int, bool) {
	switch x := v.(type) {
	case symbol:
		return len(x), true
	case binary:
		return len(x.data), true
	default:
		return 0, false
	}
}

func TestWithValueEncoding(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags"))

	defaultEval, err := evaluator.New(policy, evaluator.WithValueEncoding(encoding{}))
	require.NoError(t, err)

	runPathCases(t, defaultEval, []pathCase{
		{expr: `tags == null`, row: map[string]any{"tags": []elem(nil)}, want: false},
		{expr: `tags == null`, row: map[string]any{"tags": map[string]int(nil)}, want: false},
		{expr: `v in list`, row: map[string]any{"v": token{1}, "list": []any{token{1}}}, want: true},
		{expr: `r.name == "n"`, row: map[string]any{"r": record{Name: "n"}}, want: false},
	})
}

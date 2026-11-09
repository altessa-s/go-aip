// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
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

// TestMongoEvaluatorPaths covers the MongoDB-profile paths of the query forms.
func TestMongoEvaluatorPaths(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema(
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
			"zi": filter.FieldKindInt, "zf": filter.FieldKindFloat, "zs": filter.FieldKindString, "zb": filter.FieldKindBool,
			"zy": filter.FieldKindBytes, "zt": filter.FieldKindTimestamp, "zd": filter.FieldKindDecimal, "s.p": filter.FieldKindInt,
		}),
		mongo.WithSchemaOmitEmpty("o", "oz", "ot", "od", "os"),
		mongo.WithSchemaPresenceFields("p"))
	require.NoError(t, err)

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"zd": filter.FieldKindDecimal})), evaluator.WithProfile(schema))
	require.NoError(t, err)

	type wrapper struct{ V int }

	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	zeroTime := time.Time{}

	runPathCases(t, eval, []pathCase{
		// Zero-when-absent fields read as their kind's zero value.
		{expr: "zi == 0", row: map[string]any{}, want: true},
		{expr: "zf == 0.0", row: map[string]any{}, want: true},
		{expr: `zs == ""`, row: map[string]any{}, want: true},
		{expr: "zb == false", row: map[string]any{}, want: true},
		{expr: `zy == b""`, row: map[string]any{}, want: true},
		{expr: `zt == timestamp("0001-01-01T00:00:00Z")`, row: map[string]any{}, want: true},
		{expr: "zd == 0", row: map[string]any{}, want: true},
		{expr: "zi != 0", row: map[string]any{}, want: false},
		{expr: "has(m.zi)", row: map[string]any{"m": map[string]any{"zi": int64(2)}}, want: true},
		{expr: "zi == null", row: map[string]any{}, want: true},
		{expr: "s.p == 0", row: map[string]any{"s": []any{map[string]any{"p": int64(5)}, map[string]any{}}}, want: true},
		{expr: "s.p > 0", row: map[string]any{"s": []any{map[string]any{"p": int64(5)}}}, want: true},
		{expr: "zi in [0, 1]", row: map[string]any{}, want: true},
		{expr: `zs.contains("")`, row: map[string]any{}, want: true},
		{expr: "zi.size() == 0", row: map[string]any{}, want: false},
		// Omitted fields: empty values are absent.
		{expr: "o != null", row: map[string]any{"o": ""}, want: false},
		{expr: "o != null", row: map[string]any{"o": "x"}, want: true},
		{expr: "oz != null", row: map[string]any{"oz": int64(0)}, want: false},
		{expr: "oz == null", row: map[string]any{"oz": nil}, want: true},
		{expr: "ot != null", row: map[string]any{"ot": &zeroTime}, want: false},
		{expr: "ot != null", row: map[string]any{"ot": when}, want: true},
		{expr: "od != null", row: map[string]any{"od": decimal.Zero}, want: false},
		{expr: "os != null", row: map[string]any{"os": wrapper{}}, want: true},
		{expr: "os != null", row: map[string]any{"os": []int{}}, want: false},
		{expr: "has(os.v)", row: map[string]any{"os": map[string]any{"v": int64(1)}}, want: true},
		{expr: "o == null", row: map[string]any{"o": ""}, want: true},
		// Presence fields.
		{expr: "p == null", row: map[string]any{}, want: true},
		{expr: "p != 1", row: map[string]any{}, want: false},
		{expr: "p != null", row: map[string]any{"p": int64(1)}, want: true},
		{expr: "p == 1", row: map[string]any{"p": nil}, want: false},
		{expr: `!(p in [1])`, row: map[string]any{}, want: false},
		{expr: `!p.contains("x")`, row: map[string]any{}, want: false},
		// Queries through lists, sizes and type classes.
		{expr: "tags == null", row: map[string]any{}, want: true},
		{expr: `tags == "a"`, row: map[string]any{"tags": []any{"a"}}, want: true},
		{expr: `"a" in tags`, row: map[string]any{"tags": []any{"a"}}, want: true},
		{expr: "has(m.tags)", row: map[string]any{"m": map[string]any{"tags": []any{}}}, want: true},
		{expr: "tags.size() == 1", row: map[string]any{"tags": []any{"a"}}, want: true},
		{expr: "tags.size() > 0", row: map[string]any{}, want: false},
		{expr: "l.size() == 2", row: map[string]any{"l": []any{map[string]any{"c": []any{1, 2}}}}, want: false},
		{expr: "l.c.size() == 2", row: map[string]any{"l": []any{map[string]any{"c": []any{1, 2}}}}, want: true},
		{expr: "l.c.size() != 2", row: map[string]any{"l": []any{map[string]any{"c": "ab"}}}, want: true},
		{expr: "s.size() == 2", row: map[string]any{"s": "ab"}, want: true},
		{expr: "s.size() == 2", row: map[string]any{"s": int64(2)}, want: false},
		{expr: "s.size() != 2", row: map[string]any{"s": int64(2)}, want: true},
		{expr: "s.size() == 1.5", row: map[string]any{"s": "ab"}, want: false},
		{expr: "s.size() >= 0", row: map[string]any{}, want: true},
		{expr: `s == "x"`, row: map[string]any{"s": []any{"y", "x"}}, want: true},
		{expr: `s != "x"`, row: map[string]any{"s": []any{"y", "x"}}, want: false},
		{expr: "n > 1", row: map[string]any{"n": "2"}, want: false},
		{expr: "n == 1", row: map[string]any{"n": int32(1)}, want: true},
		{expr: "b < true", row: map[string]any{"b": false}, want: true},
		{expr: `d < b"b"`, row: map[string]any{"d": []byte("ab")}, err: filter.ErrUnsupportedOperation},
		{expr: `t < timestamp("2027-01-01T00:00:00Z")`, row: map[string]any{"t": when}, want: true},
		{expr: "n == 0.1", row: map[string]any{"n": decimal.RequireFromString("0.1")}, want: false},
		{expr: "n == 1", row: map[string]any{"n": math.NaN()}, want: false},
		{expr: `s.matches("^a")`, row: map[string]any{"s": []any{"x", "ab"}}, want: true},
		{expr: `s.endsWith("b")`, row: map[string]any{"s": int64(1)}, want: false},
		{expr: "active", row: map[string]any{"active": true}, want: true},
		{expr: "!active", row: map[string]any{}, want: true},
	})

	// Lookup reads the stored form, zero values and omitted fields.
	for _, tc := range []struct {
		field   string
		row     map[string]any
		value   any
		present bool
	}{
		{"zi", map[string]any{}, int64(0), true},
		{"o", map[string]any{"o": ""}, nil, false},
		{"n", map[string]any{"n": int8(3)}, int64(3), true},
		{"s.p", map[string]any{"s": []any{map[string]any{"p": int64(1)}}}, []any{int64(1)}, true},
	} {
		value, present, err := eval.Lookup(tc.row, tc.field)
		require.NoError(t, err, tc.field)
		require.Equal(t, tc.value, value, tc.field)
		require.Equal(t, tc.present, present, tc.field)
	}
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

	schema, err := mongo.NewSchema()
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags"))

	mongoEval, err := evaluator.New(policy, evaluator.WithProfile(schema), evaluator.WithValueEncoding(encoding{}))
	require.NoError(t, err)

	runPathCases(t, mongoEval, []pathCase{
		{expr: `s == "x"`, row: map[string]any{"s": symbol("x")}, want: true},
		{expr: `s < "y"`, row: map[string]any{"s": symbol("x")}, want: true},
		{expr: `s.startsWith("x")`, row: map[string]any{"s": symbol("xy")}, want: true},
		{expr: `d == b"ab"`, row: map[string]any{"d": binary{sub: 0, data: []byte("ab")}}, want: true},
		{expr: `d == b"ab"`, row: map[string]any{"d": binary{sub: 4, data: []byte("ab")}}, want: false},
		{expr: `d < b"abc"`, row: map[string]any{"d": binary{sub: 4, data: []byte("ab")}}, err: filter.ErrUnsupportedOperation},
		{expr: `r.name == "n"`, row: map[string]any{"r": record{Name: "n"}}, want: true},
		{expr: `r.name == "n"`, row: map[string]any{"r": &record{Name: "n"}}, want: true},
		{expr: `e.k == 1`, row: map[string]any{"e": []elem{{Key: "k", Value: int64(1)}}}, want: true},
		// Expression forms over the encoding's types.
		{expr: `a == b`, row: map[string]any{"a": symbol("x"), "b": "x"}, want: true},
		{expr: `a < b`, row: map[string]any{"a": symbol("x"), "b": "y"}, want: false},
		{expr: `a < b`, row: map[string]any{"a": symbol("x"), "b": symbol("y")}, want: true},
		{expr: `a == b`, row: map[string]any{"a": token{1}, "b": token{1}}, want: true},
		{expr: `a == b`, row: map[string]any{"a": token{1}, "b": token{2}}, want: false},
		{expr: `a < b`, row: map[string]any{"a": token{1}, "b": token{2}}, want: false},
		{expr: `a < b`, row: map[string]any{"a": binary{sub: 1, data: []byte("a")}, "b": binary{sub: 2, data: []byte("a")}}, want: true},
		{expr: `a.contains(b)`, row: map[string]any{"a": symbol("abc"), "b": "b"}, want: false},
		{expr: `a == b`, row: map[string]any{"a": []any{token{1}}, "b": []any{token{1}}}, want: true},
	})
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// Go types the default evaluator does not know, read as the values they are.
type (
	readingBytes  []byte
	readingRecord struct{ X int64 }
	readingError  struct{}
)

func (*readingError) Error() string { return "reading" }

// readingExprs are the predicates of the default reading matrix, in the order of the codes of [readingRows].
var readingExprs = []string{
	`v == null`, `v != null`, `size(v) == 0`, `v == b""`, `v != b""`, `v < b"a"`, `v == "a"`, `v == 5`, `v == true`, `v in [null]`,
	`v in [b""]`, `v in ["a", 5]`, `"a" in v`, `null in v`, `v.contains("")`, `v.matches(".*")`, `v.substring(0) == ""`,
	`has(v.x)`, `v.x == null`, `v.x != null`, `v.x == 1`, `size(v.x) == 0`, `v.x == b""`, `v.x in [null]`,
}

// readingRow is a data value v, absent when absent, with the outcome of every predicate of [readingExprs] on it, one code each:
// t or f the result (f also for a data-driven unknown, which does not match), O ErrUnsupportedOperation, I ErrInvalidExpression, U ErrUnsupportedType — the evaluator refusing the type of
// the value, which the default evaluator now reads in its stored form instead, so any outcome is accepted.
type readingRow struct {
	name   string
	v      any
	absent bool
	codes  string
}

// readingRows returns the default reading matrix: the outcomes the default evaluator gave before it read values in their stored
// form (go-atlas c20b9d1f), which it keeps.
func readingRows() []readingRow {
	var (
		nilList []any
		nilAny  any
	)

	five := 5

	return []readingRow{
		{name: "absent", absent: true, codes: "tffftfffftffIIfffftfffft"},
		{name: "nil", v: nil, codes: "tffftfffftffIIfffftfffft"},
		{name: "[]any(nil)", v: []any(nil), codes: "fttUUUUUUfffffffIftfffft"},
		{name: "[]any{}", v: []any{}, codes: "fttUUUUUUfffffffIftfffft"},
		{name: "[]any{nil}", v: []any{nil}, codes: "ftfUUUUUUfffftffIftfffft"},
		{name: "[]string(nil)", v: []string(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "[]string{}", v: []string{}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "[]string{a}", v: []string{"a"}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "[]int(nil)", v: []int(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "map[string]any(nil)", v: map[string]any(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "map[string]any{}", v: map[string]any{}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "map[string]string(nil)", v: map[string]string(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "map[string]string{x: a}", v: map[string]string{"x": "a"}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "[]byte(nil)", v: []byte(nil), codes: "ftOtftUUUftfIIffIftfffft"},
		{name: "[]byte{}", v: []byte{}, codes: "ftOtftUUUftfIIffIftfffft"},
		{name: "named bytes nil", v: readingBytes(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "named bytes empty", v: readingBytes{}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "*int(nil)", v: (*int)(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "*string(nil)", v: (*string)(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "pointer to a nil []any", v: &nilList, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "pointer to a nil any", v: &nilAny, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "pointer to 5", v: &five, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "typed nil error", v: (*readingError)(nil), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "struct", v: readingRecord{X: 1}, codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "named string", v: label("a"), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "int8", v: int8(5), codes: "ftOUUUUUUfffIIffIftfffft"},
		{name: "{x: 1}", v: map[string]any{"x": int64(1)}, codes: "ftOUUUUUUfffIIffItfttOUf"},
		{name: "{x: nil}", v: map[string]any{"x": nil}, codes: "ftOUUUUUUfffIIffIttfffft"},
		{name: "{x: []any(nil)}", v: map[string]any{"x": []any(nil)}, codes: "ftOUUUUUUfffIIffItftUtUf"},
		{name: "{x: []byte(nil)}", v: map[string]any{"x": []byte(nil)}, codes: "ftOUUUUUUfffIIffItftUOtf"},
		{name: "{x: *int(nil)}", v: map[string]any{"x": (*int)(nil)}, codes: "ftOUUUUUUfffIIffItftUOUf"},
		{name: "{x: map[string]any(nil)}", v: map[string]any{"x": map[string]any(nil)}, codes: "ftOUUUUUUfffIIffItftUOUf"},
		{name: "{x: []string(nil)}", v: map[string]any{"x": []string(nil)}, codes: "ftOUUUUUUfffIIffItftUOUf"},
	}
}

// readingCode returns the code of an outcome, as [readingRow] spells it.
func readingCode(got bool, err error) string {
	switch {
	case err == nil && got:
		return "t"
	case err == nil:
		return "f"
	case errors.Is(err, filter.ErrUnsupportedType):
		return "U"
	case errors.Is(err, filter.ErrUnsupportedOperation):
		return "O"
	case errors.Is(err, filter.ErrInvalidExpression):
		return "I"
	default:
		return err.Error()
	}
}

// TestEvaluator_DefaultReadsGoValues: review finding R20-2 — the default evaluator read every data value in its stored form, so a
// nil slice or map of any type, a nil pointer and a bson.Null became null, and a path stepped through a bson.M, a bson.D, a struct
// or a typed map: size(tags) == 0 on a nil []any failed with ErrUnsupportedOperation, a nil []byte equaled no b"", `tags == null`
// held for a nil slice (verified on eaf9d227: 118 of the outcomes below differ). Without WithProfile the evaluator reads a
// value as the Go value it is, with the outcome it always had; only a value whose type it refused (ErrUnsupportedType) may now be
// compared in its stored form.
func TestEvaluator_DefaultReadsGoValues(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator()
	require.NoError(t, err)

	for _, row := range readingRows() {
		require.Len(t, row.codes, len(readingExprs), row.name)

		data := map[string]any{"v": row.v}
		if row.absent {
			data = map[string]any{}
		}

		for i, expr := range readingExprs {
			want := row.codes[i : i+1]

			got := readingCode(eval.Evaluate(filtertest.MustParseFilter(t, expr), data))
			if want != "U" {
				require.Equal(t, want, got, "%s: %s", row.name, expr)
			}
		}
	}
}

// TestEvaluator_DefaultNilIsEmpty: on a field declared by WithArrayFields, which the default evaluator reads in its stored form,
// a nil slice, map or byte slice of any type evaluates as the empty one, never as null, as a nil []any always has; under MongoDB
// semantics it is null, as the driver stores it (TestEvaluator_NilSliceIsNull).
func TestEvaluator_DefaultNilIsEmpty(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator(withPolicy(filter.WithArrayFields("v", "v.x")))
	require.NoError(t, err)

	pairs := []struct{ nilValue, empty any }{
		{[]any(nil), []any{}}, {[]string(nil), []string{}}, {[]int(nil), []int{}},
		{map[string]any(nil), map[string]any{}}, {map[string]string(nil), map[string]string{}},
		{[]byte(nil), []byte{}}, {readingBytes(nil), readingBytes{}},
		{map[string]any{"x": []string(nil)}, map[string]any{"x": []string{}}},
		{map[string]any{"x": map[string]any(nil)}, map[string]any{"x": map[string]any{}}},
	}
	for _, pair := range pairs {
		for _, expr := range readingExprs {
			node := filtertest.MustParseFilter(t, expr)
			want := readingCode(eval.Evaluate(node, map[string]any{"v": pair.empty}))
			got := readingCode(eval.Evaluate(node, map[string]any{"v": pair.nilValue}))
			require.Equal(t, want, got, "%#v: %s", pair.nilValue, expr)
		}
	}

	for _, v := range []any{[]any(nil), []string(nil), []byte(nil), (*int)(nil)} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, "v == null"), map[string]any{"v": v})
		require.NoError(t, err)
		require.False(t, got, "%#v", v)
	}
}

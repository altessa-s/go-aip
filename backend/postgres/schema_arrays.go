// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"cmp"
	"math"
	"reflect"
	"slices"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

// Dimension preserves PostgreSQL array length and lower bound.
type Dimension struct {
	Length     int
	LowerBound int
}

// ArrayData represents a SQL array when native slices cannot preserve lower bounds.
// Elements are flattened in row-major order; Valid=false represents SQL NULL.
type ArrayData struct {
	Elements   []any
	Dimensions []Dimension
	Valid      bool
}

// convertDimensions copies dimensions between the public [Dimension] and the compiled [sqltype.Dimension], keeping a nil
// slice nil.
func convertDimensions[To, From ~struct{ Length, LowerBound int }](in []From) []To {
	if in == nil {
		return nil
	}

	out := make([]To, len(in))
	for i, dim := range in {
		out[i] = To(dim)
	}

	return out
}

type arrayValue struct {
	values     []any
	dimensions []sqltype.Dimension
}

func readArray(v any, k sqlbase.Kind, d evaluator.ValueDecoder, b *budget, depth int) (arrayValue, error) {
	if explicit, ok := v.(ArrayData); ok {
		return readExplicitArray(explicit, k, d, b)
	}

	if depth >= b.options.maxArrayDimensions {
		return arrayValue{}, filter.ErrDataLimitExceeded
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return arrayValue{}, typeError()
	}

	if err := b.Spend(rv.Len()); err != nil {
		return arrayValue{}, err
	}

	out := arrayValue{dimensions: []sqltype.Dimension{{Length: rv.Len(), LowerBound: 1}}, values: make([]any, 0, rv.Len())}
	for i := 0; i < rv.Len(); i++ {
		item, err := unwrap(rv.Index(i).Interface(), d, b)
		if err != nil {
			return out, err
		}

		r := reflect.ValueOf(item)

		nested := isNestedArray(r, k)
		if nested {
			a, err := readArray(item, k, d, b, depth+1)
			if err != nil {
				return out, err
			}

			if i == 0 {
				out.dimensions = append(out.dimensions, a.dimensions...)
			} else if !slices.Equal(out.dimensions[1:], a.dimensions) {
				return out, typeError()
			}

			out.values = append(out.values, a.values...)
		} else {
			if len(out.dimensions) > 1 {
				return out, typeError()
			}

			value, err := normalize(item, k, d, b)
			if err != nil {
				return out, err
			}

			out.values = append(out.values, value)
		}
	}

	return out, nil
}

// arrayPredicate compares the elements of array a with the scalar c: != holds when no element equals c, every other operator
// when some element satisfies it. A NULL array has no elements and a NULL element satisfies nothing, as in the default
// evaluator. A NULL c (a null value) satisfies nothing in an empty array and is unknown for any other: SQL's = ANY cannot find
// it equal to a NULL element, as the default evaluator does.
//
//nolint:nilnil // nil is an unknown result.
func arrayPredicate(op filter.Operator, a, c any, k sqlbase.Kind, b *budget) (any, error) {
	var values arrayValue

	if a != nil {
		var ok bool
		if values, ok = a.(arrayValue); !ok {
			return nil, typeError()
		}
	}

	if c == nil && len(values.values) != 0 {
		return nil, nil
	}

	match := op
	if match == filter.OpNotEqual {
		match = filter.OpEqual
	}

	found, err := anyElementComparison(match, values, c, k, b)
	if err != nil {
		return nil, err
	}

	return found != (op == filter.OpNotEqual), nil
}

// anyComparison reports whether a equals some non-NULL element of values. A NULL (absent) array has no elements; a NULL a
// is in no empty array and unknown for any other, see [arrayPredicate].
//
//nolint:nilnil // nil is an unknown result.
func anyComparison(a any, values arrayValue, k sqlbase.Kind, b *budget) (any, error) {
	if a == nil {
		if len(values.values) != 0 {
			return nil, nil
		}

		return false, nil
	}

	return anyElementComparison(filter.OpEqual, values, a, k, b)
}

// anyElementComparison reports whether some non-NULL element of values satisfies op with right, which compareValue is never
// given NULL: an empty values has no element to compare.
func anyElementComparison(op filter.Operator, values arrayValue, right any, k sqlbase.Kind, b *budget) (bool, error) {
	for _, v := range values.values {
		if v == nil {
			continue
		}

		n, err := compareValue(v, right, k, b)
		if err != nil {
			return false, err
		}

		if comparison(op, n) {
			return true, nil
		}
	}

	return false, nil
}

func compareArrays(x, y arrayValue, k sqlbase.Kind, b *budget) (int, error) {
	for i := 0; i < min(len(x.values), len(y.values)); i++ {
		u, v := x.values[i], y.values[i]
		if u == nil || v == nil {
			if u == nil && v == nil {
				continue
			}

			if u == nil {
				return 1, nil
			}

			return -1, nil
		}

		n, err := compareValue(u, v, k, b)
		if err != nil || n != 0 {
			return n, err
		}
	}

	if n := cmp.Compare(len(x.values), len(y.values)); n != 0 {
		return n, nil
	}

	if len(x.values) == 0 {
		return 0, nil
	}

	if n := cmp.Compare(len(x.dimensions), len(y.dimensions)); n != 0 {
		return n, nil
	}

	for i, d := range x.dimensions {
		if n := cmp.Compare(d.Length, y.dimensions[i].Length); n != 0 {
			return n, nil
		}
	}

	for i, d := range x.dimensions {
		if n := cmp.Compare(d.LowerBound, y.dimensions[i].LowerBound); n != 0 {
			return n, nil
		}
	}

	return 0, nil
}

// coerceArray matches an explicit SQL array cast, including conversion errors
// in elements beyond the first unequal pair used by a lexicographic comparison.
func coerceArray(v any, k sqlbase.Kind, b *budget) (arrayValue, error) {
	a, ok := v.(arrayValue)
	if !ok {
		return arrayValue{}, typeError()
	}

	if err := b.Spend(len(a.values)); err != nil {
		return arrayValue{}, err
	}

	values := make([]any, len(a.values))
	for i, item := range a.values {
		value, err := normalize(item, k, nil, b)
		if err != nil {
			return arrayValue{}, err
		}

		values[i] = value
	}

	return arrayValue{values: values, dimensions: a.dimensions}, nil
}

// arrayStringCall applies a string function to the elements of array a: true when some element satisfies it. A NULL array
// has no elements and a NULL element satisfies nothing; a NULL argument is unknown, as the default evaluator finds it even for
// an empty array.
//
//nolint:nilnil // A NULL argument is unknown.
func arrayStringCall(op filter.Operator, a, c any, b *budget, matcher filter.RegexMatcher) (any, error) {
	if c == nil {
		return nil, nil
	}

	if a == nil {
		return false, nil
	}

	values, ok := a.(arrayValue)
	if !ok {
		return nil, typeError()
	}

	for _, v := range values.values {
		if v == nil {
			continue
		}

		result, err := stringCall(op, v, c, b, matcher)
		if err != nil {
			return nil, err
		}

		if result == true {
			return true, nil
		}
	}

	return false, nil
}

func readExplicitArray(input ArrayData, k sqlbase.Kind, d evaluator.ValueDecoder, b *budget) (arrayValue, error) {
	if len(input.Dimensions) > b.options.maxArrayDimensions {
		return arrayValue{}, typeError()
	}

	if err := b.Spend(len(input.Elements) + len(input.Dimensions)); err != nil {
		return arrayValue{}, err
	}

	total := 1
	if len(input.Dimensions) == 0 {
		total = 0
	}

	for _, dim := range input.Dimensions {
		if dim.LowerBound < math.MinInt32 || dim.LowerBound > math.MaxInt32 || int64(dim.LowerBound)+int64(dim.Length)-1 > math.MaxInt32 {
			return arrayValue{}, typeError()
		}

		if dim.Length < 0 || dim.Length > len(input.Elements) || dim.Length != 0 && total > len(input.Elements)/dim.Length {
			return arrayValue{}, typeError()
		}

		total *= dim.Length
	}

	if total != len(input.Elements) {
		return arrayValue{}, typeError()
	}

	out := arrayValue{values: make([]any, total), dimensions: convertDimensions[sqltype.Dimension](input.Dimensions)}
	for i, item := range input.Elements {
		value, err := normalize(item, k, d, b)
		if err != nil {
			return arrayValue{}, err
		}

		out.values[i] = value
	}

	return out, nil
}

func isNestedArray(value reflect.Value, k sqlbase.Kind) bool {
	if !value.IsValid() || (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) {
		return false
	}

	if k == sqlbase.UUID && value.Type() == reflect.TypeFor[[16]byte]() {
		return false
	}

	if k == sqlbase.JSON {
		return false
	}

	return k != sqlbase.Bytes || value.Type().Elem().Kind() != reflect.Uint8
}

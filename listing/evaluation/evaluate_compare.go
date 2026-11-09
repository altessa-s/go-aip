// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"bytes"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/filter"
)

func (e *contractEvaluator) compare(x *compareExpr, data map[string]any) (truth, error) {
	value, err := e.field(x.ref, data)
	if err != nil {
		return falseValue, err
	}

	if x.size {
		value = sizeOf(value, x.ref.info.Array)
	}

	if x.rightErr != nil {
		return falseValue, x.rightErr
	}

	op, right := x.op, x.right

	if x.size {
		return truthOf(scalarCompare(op, value, right)), nil
	}

	if x.ref.info.Presence {
		if t, ok := presenceCompare(op, value, right); ok {
			return t, nil
		}
	}

	if x.ref.info.Array {
		return e.arrayCompare(op, value, right), nil
	}

	if right == nil && isEquality(op) {
		right = x.ref.zero
	}

	return truthOf(scalarCompare(op, value, right)), nil
}

// sizeOf returns size() of a value: a code point count for strings and an element count for arrays.
func sizeOf(value any, array bool) int64 {
	if array {
		return int64(len(elements(value)))
	}

	if text, ok := value.(string); ok {
		return int64(utf8.RuneCountInString(text))
	}

	return 0
}

func isEquality(op filter.Operator) bool { return op == filter.OpEqual || op == filter.OpNotEqual }

// presenceCompare decides a comparison of a presence field when the value or the operand is null; ok is false otherwise.
func presenceCompare(op filter.Operator, value, right any) (t truth, ok bool) {
	if right == nil && isEquality(op) {
		return truthOf((value == nil) == (op == filter.OpEqual)), true
	}

	if value == nil && right != nil {
		// A null value differs from every value, as in the default evaluator; an ordering comparison with it is unknown.
		switch op {
		case filter.OpEqual:
			return falseValue, true
		case filter.OpNotEqual:
			return trueValue, true
		default:
			return unknownValue, true
		}
	}

	return falseValue, false
}

// arrayCompare compares the elements of an array field with an operand: equality holds when an element is equal, inequality when
// none is, an ordering when an element is ordered so; a null operand tests for an empty array.
func (e *contractEvaluator) arrayCompare(op filter.Operator, value, right any) truth {
	if right == nil && isEquality(op) {
		return truthOf((len(elements(value)) == 0) == (op == filter.OpEqual))
	}

	found := e.anyElement(value, true, func(item any) bool { return scalarCompare(filter.OpEqual, item, right) })

	switch op {
	case filter.OpNotEqual:
		return truthOf(!found)
	case filter.OpEqual:
		return truthOf(found)
	default:
		return truthOf(e.anyElement(value, true, func(item any) bool { return scalarCompare(op, item, right) }))
	}
}

func scalarCompare(op filter.Operator, a, b any) bool {
	a, b = normalizeScalar(a), normalizeScalar(b)
	if op == filter.OpEqual {
		return equal(a, b)
	}

	if op == filter.OpNotEqual {
		return !equal(a, b)
	}

	if at, ok := a.(time.Time); ok {
		bt, ok := b.(time.Time)
		if !ok {
			return false
		}

		return compareResult(op, at.Compare(bt))
	}

	if c, ok := numericOrder(a, b); ok {
		return compareResult(op, c)
	}

	if x, ok := a.(string); ok {
		y, ok := b.(string)
		if !ok {
			return false
		}

		cmp := strings.Compare(x, y)

		return compareResult(op, cmp)
	}

	if x, ok := a.([]byte); ok {
		y, ok := b.([]byte)
		if !ok {
			return false
		}

		cmp := bytes.Compare(x, y)

		return compareResult(op, cmp)
	}

	if x, ok := a.(bool); ok {
		y, ok := b.(bool)
		if !ok {
			return false
		}

		cmp := 0

		if x != y {
			if x {
				cmp = 1
			} else {
				cmp = -1
			}
		}

		return compareResult(op, cmp)
	}

	return false
}

func compareResult(op filter.Operator, c int) bool {
	switch op {
	case filter.OpLT:
		return c < 0
	case filter.OpLTE:
		return c <= 0
	case filter.OpGT:
		return c > 0
	case filter.OpGTE:
		return c >= 0
	default:
		return false
	}
}

func equal(a, b any) bool {
	a, b = normalizeScalar(a), normalizeScalar(b)
	switch x := a.(type) {
	case string:
		// Neither a number, a time nor bytes: reflect.DeepEqual reduces to this.
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}

	if x, ok := a.(time.Time); ok {
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}

	if c, ok := numericOrder(a, b); ok {
		return c == 0
	}

	if x, ok := a.([]byte); ok {
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	}

	return reflect.DeepEqual(a, b)
}

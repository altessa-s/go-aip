// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"bytes"
	"cmp"
	"math"
	"time"

	"github.com/altessa-s/go-aip/internal/typename"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// compare performs typed comparison.
func compare(op filter.Operator, left, right any) (bool, error) {
	// Two strings or two int64 values, the most common operands, compare as the general path below finds them.
	switch l := left.(type) {
	case string:
		if r, ok := right.(string); ok {
			return compareOrdered(op, l, r)
		}
	case int64:
		if r, ok := right.(int64); ok {
			return compareOrdered(op, l, r)
		}
	}
	// Handle nil
	if left == nil || right == nil {
		return compareNil(op, left, right)
	}
	// A decimal compares exactly, never through float64.
	if c, ok, err := compareDecimal(left, right); ok {
		if err != nil {
			return false, coreerrs.Wrapf(filter.ErrUnsupportedType, "cannot compare %s and %s: %w", typename.Of(left), typename.Of(right), err)
		}

		return CompareResult(op, c)
	}

	if l, ok := left.(time.Time); ok {
		if r, ok := right.(time.Time); ok {
			return CompareResult(op, l.Compare(r))
		}
	}

	if l, ok := left.([]byte); ok {
		if r, ok := right.([]byte); ok {
			return CompareResult(op, bytes.Compare(l, r))
		}
	}

	// Numbers compare exactly: through float64, distinct integers above 2^53
	// would collapse into one, and into the float between them.
	if c, ok := compareReals(left, right); ok {
		return CompareResult(op, c)
	}

	// A NaN operand: IEEE 754 comparison, whatever the other number's value.
	ln, lok := toFloat64(left)

	rn, rok := toFloat64(right)
	if lok && rok {
		return compareOrdered(op, ln, rn)
	}

	// String comparison
	ls, lok := left.(string)

	rs, rok := right.(string)
	if lok && rok {
		return compareOrdered(op, ls, rs)
	}

	// Bool comparison (== and != only)
	lb, lok := left.(bool)

	rb, rok := right.(bool)
	if lok && rok {
		switch op {
		case filter.OpEqual:
			return lb == rb, nil
		case filter.OpNotEqual:
			return lb != rb, nil
		default:
			return false, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "cannot use %v on booleans", op)
		}
	}

	return false, coreerrs.Wrapf(filter.ErrUnsupportedType, "cannot compare %s and %s", typename.Of(left), typename.Of(right))
}

// compareNil compares operands of which one is nil: == and != hold as null equals only null; an ordered comparison is a
// data-driven unknown ([unknownError]).
func compareNil(op filter.Operator, left, right any) (bool, error) {
	switch op {
	case filter.OpEqual:
		return left == nil && right == nil, nil
	case filter.OpNotEqual:
		return left != nil || right != nil, nil
	default:
		return false, nullComparisonUnknown(op)
	}
}

type ordered interface {
	~int64 | ~float64 | ~string
}

func compareOrdered[T ordered](op filter.Operator, a, b T) (bool, error) {
	switch op {
	case filter.OpEqual:
		return a == b, nil
	case filter.OpNotEqual:
		return a != b, nil
	case filter.OpLT:
		return a < b, nil
	case filter.OpLTE:
		return a <= b, nil
	case filter.OpGT:
		return a > b, nil
	case filter.OpGTE:
		return a >= b, nil
	default:
		return false, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "operator %v", op)
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// valuesEqual reports whether a and b are equal for the in operator. Only the forms [storedValue] returns compare: numbers by
// value, timestamps by instant, bytes by content, and strings, bools and the BSON types storedValue keeps by value. Any other
// value — an opaque one, a document, an array — equals nothing, without reflection: reflect.Value.Comparable walks every element
// of an array or struct, and an opaque data value may be a [1<<30]struct{}. Bytes compared one by one are charged by the caller
// ([dataBudget.chargeComparison]).
func valuesEqual(enc ValueEncoding, a, b any) bool {
	if c, ok, err := compareDecimal(a, b); ok { // a decimal equals the number a string or a float spells
		return err == nil && c == 0
	}

	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	}

	if c, ok := compareReals(a, b); ok {
		return c == 0
	}

	an, aok := toFloat64(a)

	bn, bok := toFloat64(b)
	if aok && bok {
		return an == bn
	}

	ext, ok := enc.(ScalarEquality)

	return ok && ext.Equal(a, b)
}

// integerParts splits an integer operand into its signed or unsigned value.
// ok is false for any non-integer.
func integerParts(v any) (signed int64, unsigned uint64, isUnsigned, ok bool) {
	switch n := v.(type) {
	case int64:
		return n, 0, false, true
	case int32:
		return int64(n), 0, false, true
	case int:
		return int64(n), 0, false, true
	case uint64:
		return 0, n, true, true
	default:
		return 0, 0, false, false
	}
}

// compareIntegers compares two integer operands exactly, signed against
// unsigned included, returning -1, 0 or +1. ok is false unless both are
// integers.
func compareIntegers(a, b any) (int, bool) {
	as, au, aUnsigned, aok := integerParts(a)

	bs, bu, bUnsigned, bok := integerParts(b)
	if !aok || !bok {
		return 0, false
	}

	switch {
	case !aUnsigned && !bUnsigned:
		return cmp.Compare(as, bs), true
	case aUnsigned && bUnsigned:
		return cmp.Compare(au, bu), true
	case aUnsigned: // b signed
		if bs < 0 {
			return 1, true
		}

		return cmp.Compare(au, uint64(bs)), true
	default: // a signed, b unsigned
		if as < 0 {
			return -1, true
		}

		return cmp.Compare(uint64(as), bu), true
	}
}

// compareReals compares two operands of the integer and float64 kinds exactly, returning -1, 0 or +1: an integer is never
// rounded to a float64, so 2^53+1 stays above the float 2^53, and the infinities order beyond every integer. ok is false when an
// operand is not such a number or is NaN.
func compareReals(a, b any) (int, bool) {
	if c, ok := compareIntegers(a, b); ok {
		return c, true
	}

	af, aFloat := a.(float64)

	bf, bFloat := b.(float64)
	switch {
	case aFloat && bFloat:
		if math.IsNaN(af) || math.IsNaN(bf) {
			return 0, false
		}

		return cmp.Compare(af, bf), true
	case bFloat:
		return compareIntegerFloat(a, bf)
	case aFloat:
		c, ok := compareIntegerFloat(b, af)
		return -c, ok
	default:
		return 0, false
	}
}

// Float64 bounds of the integer ranges, both exact powers of two.
const (
	float2To63 = 0x1p63
	float2To64 = 0x1p64
)

// compareIntegerFloat compares the integer i with f exactly. ok is false when i is not an integer or f is NaN.
func compareIntegerFloat(i any, f float64) (int, bool) {
	signed, unsigned, isUnsigned, ok := integerParts(i)
	if !ok || math.IsNaN(f) {
		return 0, false
	}
	// Beyond the integer range (the infinities included) f orders by its sign. Within it, f has an exact integer part, and an
	// integer equal to that part orders against f by f's fraction.
	whole := math.Trunc(f)

	var c int

	switch {
	case isUnsigned && f < 0, !isUnsigned && f < -float2To63:
		return 1, true
	case isUnsigned && f >= float2To64, !isUnsigned && f >= float2To63:
		return -1, true
	case isUnsigned:
		c = cmp.Compare(unsigned, uint64(whole))
	default:
		c = cmp.Compare(signed, int64(whole))
	}

	if c != 0 {
		return c, true
	}

	return cmp.Compare(whole, f), true
}

// CompareResult applies the comparison operator op to the outcome c of a three-way comparison (negative, zero or positive), as
// the evaluator does; it fails with [filter.ErrUnsupportedOperation] for an operator that is not a comparison.
func CompareResult(op filter.Operator, c int) (bool, error) {
	switch op {
	case filter.OpEqual:
		return c == 0, nil
	case filter.OpNotEqual:
		return c != 0, nil
	case filter.OpLT:
		return c < 0, nil
	case filter.OpLTE:
		return c <= 0, nil
	case filter.OpGT:
		return c > 0, nil
	case filter.OpGTE:
		return c >= 0, nil
	default:
		return false, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "operator %v", op)
	}
}

// CompareExact orders a against b by their exact values, as the evaluator compares numbers: when either is a decimal.Decimal,
// both as the decimals [filter.DecimalLiteral] reads (err when it cannot read one); otherwise two integers (int64, int32, int,
// uint64) or float64 values, an integer never rounded to a float64. ok is false for any other pair, and for a float64 NaN.
func CompareExact(a, b any) (c int, ok bool, err error) {
	if c, ok, err = compareDecimal(a, b); ok {
		return c, true, err
	}

	c, ok = compareReals(a, b)

	return c, ok, nil
}

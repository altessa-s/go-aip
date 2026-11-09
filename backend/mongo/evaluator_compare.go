// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"bytes"
	"cmp"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

// binaryGeneric is the generic binary subtype, the one a []byte is stored as.
const binaryGeneric byte = 0x00

// mongoCompare reports whether a op b holds in a MongoDB query. Values compare only within a type class — numbers (integers,
// floats and decimals together), strings, bools (false first), timestamps (at millisecond precision) and bytes; null equals only
// null (and an absent value). Values that do not compare satisfy != and nothing else.
func mongoCompare(enc evaluator.ValueEncoding, op filter.Operator, a, b any) bool {
	c, ok := mongoOrder(enc, a, b)
	if !ok {
		return op == filter.OpNotEqual
	}

	holds, _ := evaluator.CompareResult(op, c) //nolint:errcheck // op is a comparison operator

	return holds
}

// mongoOrder orders a against b within their type class; ok is false when they do not compare.
func mongoOrder(enc evaluator.ValueEncoding, a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, a == nil && b == nil
	}

	if aNumber, bNumber := isMongoNumber(a), isMongoNumber(b); aNumber || bNumber {
		if !aNumber || !bNumber {
			return 0, false
		}

		return compareNumbers(a, b)
	}

	if x, ok := mongoString(enc, a); ok {
		y, ok := mongoString(enc, b)
		return strings.Compare(x, y), ok
	}

	switch x := a.(type) {
	case bool:
		y, ok := b.(bool)
		return cmp.Compare(boolRank(x), boolRank(y)), ok
	case time.Time:
		y, ok := b.(time.Time)
		return cmp.Compare(x.UnixMilli(), y.UnixMilli()), ok
	default:
		xSub, xData, ok := binaryParts(enc, a)
		if !ok {
			return 0, false
		}

		ySub, yData, ok := binaryParts(enc, b)
		if !ok {
			return 0, false
		}

		return compareBinary(xSub, xData, ySub, yData), true
	}
}

// mongoString returns v as a string when it is in MongoDB's string class: a string, or a symbol of the encoding
// ([evaluator.StringExtractor]).
func mongoString(enc evaluator.ValueEncoding, v any) (string, bool) {
	if x, ok := v.(string); ok {
		return x, true
	}

	if enc == nil {
		return "", false
	}

	if ext, ok := enc.(evaluator.StringExtractor); ok {
		return ext.StringValue(v)
	}

	return "", false
}

// binaryParts returns the subtype and data of a BSON binary: a []byte is the generic subtype; a binary of another subtype is the
// encoding's ([evaluator.BinaryExtractor], kept by [evaluator.StoredValue]). ok is false for any other value.
func binaryParts(enc evaluator.ValueEncoding, v any) (subtype byte, data []byte, ok bool) {
	if x, isBytes := v.([]byte); isBytes {
		return binaryGeneric, x, true
	}

	if enc == nil {
		return 0, nil, false
	}

	if ext, ok := enc.(evaluator.BinaryExtractor); ok {
		return ext.BinaryValue(v)
	}

	return 0, nil, false
}

// compareBinary orders two BSON binaries as MongoDB does: by length, then subtype, then bytes.
func compareBinary(aSub byte, a []byte, bSub byte, b []byte) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}

	if c := cmp.Compare(aSub, bSub); c != 0 {
		return c
	}

	return bytes.Compare(a, b)
}

// isMongoNumber reports whether v is in MongoDB's number class.
func isMongoNumber(v any) bool {
	switch v.(type) {
	case int64, int32, int, uint64, float64, decimal.Decimal:
		return true
	default:
		return false
	}
}

// compareNumbers orders two numbers by their exact values, as MongoDB does: an integer is never rounded to a double, and a double
// compares with a decimal by its binary value (the double 0.1 is above the decimal 0.1). NaN equals NaN and is unordered against
// any other number.
func compareNumbers(a, b any) (int, bool) {
	if c, ok, handled := compareDecimalFloat(a, b); handled {
		return c, ok
	}

	if c, ok, err := evaluator.CompareExact(a, b); ok {
		return c, err == nil
	}

	x, _ := a.(float64)
	y, _ := b.(float64)

	return 0, math.IsNaN(x) && math.IsNaN(y)
}

// compareDecimalFloat compares a decimal.Decimal with a float64 (in either order) by the exact binary value of the float; handled
// is false for any other pair. ok is false when the float is NaN or the decimal is not a Decimal128 ([filter.DecimalLiteral]).
func compareDecimalFloat(a, b any) (c int, ok, handled bool) {
	d, isDecimal := a.(decimal.Decimal)
	f, isFloat := b.(float64)
	sign := 1

	if !isDecimal || !isFloat {
		d, isDecimal = b.(decimal.Decimal)
		f, isFloat = a.(float64)
		sign = -1
	}

	if !isDecimal || !isFloat {
		return 0, false, false
	}

	bounded, err := filter.DecimalLiteral(d)
	switch {
	case err != nil || math.IsNaN(f):
		return 0, false, true
	case math.IsInf(f, 0):
		return sign * -int(math.Copysign(1, f)), true, true
	}

	return sign * bounded.Rat().Cmp(new(big.Rat).SetFloat64(f)), true, true
}

// zeroOf returns the value the evaluator reads for a field of kind declared by [WithSchemaZeroWhenAbsent] when the data lacks it.
func zeroOf(kind filter.FieldKind) any {
	switch kind {
	case filter.FieldKindInt:
		return int64(0)
	case filter.FieldKindFloat:
		return float64(0)
	case filter.FieldKindString:
		return ""
	case filter.FieldKindBool:
		return false
	case filter.FieldKindBytes:
		return []byte{}
	case filter.FieldKindTimestamp:
		return time.Time{}
	case filter.FieldKindDecimal:
		return decimal.Zero
	default:
		return nil
	}
}

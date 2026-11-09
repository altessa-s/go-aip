// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"cmp"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/filter/evaluator"
)

// BSON comparison order ranks of the value classes an expression compares across types.
const (
	rankNull = iota + 1
	rankNumber
	rankString
	rankDocument
	rankArray
	rankBinary
	rankOther // a type of the encoding without a known order (an ObjectID, a regular expression, ...)
	rankBool
	rankTimestamp
)

// exprRank is the rank of v in the BSON comparison order.
func exprRank(enc evaluator.ValueEncoding, v any) int {
	switch v.(type) {
	case nil:
		return rankNull
	case map[string]any:
		return rankDocument
	case []any:
		return rankArray
	case bool:
		return rankBool
	case time.Time:
		return rankTimestamp
	}

	switch {
	case isMongoNumber(v):
		return rankNumber
	case isMongoStringClass(enc, v):
		return rankString
	case isBinary(enc, v):
		return rankBinary
	default:
		return rankOther
	}
}

// isMongoStringClass reports whether v is a string or a symbol of the encoding.
func isMongoStringClass(enc evaluator.ValueEncoding, v any) bool {
	_, ok := mongoString(enc, v)
	return ok
}

// isBinary reports whether v is a BSON binary.
func isBinary(enc evaluator.ValueEncoding, v any) bool {
	_, _, ok := binaryParts(enc, v)
	return ok
}

// sameTypeClass reports whether an ordering comparison of a and b holds a chance under $expr: both are numbers, or of one BSON type
// ($type), which keeps a symbol apart from a string and the types of the encoding apart from each other.
func sameTypeClass(enc evaluator.ValueEncoding, a, b any) bool {
	if isMongoNumber(a) && isMongoNumber(b) {
		return true
	}

	ra, rb := exprRank(enc, a), exprRank(enc, b)
	if ra != rb {
		return false
	}

	switch ra {
	case rankString:
		_, aString := a.(string)
		_, bString := b.(string)

		return aString == bString
	case rankOther:
		return reflect.TypeOf(a) == reflect.TypeOf(b) // the Go type tells the types of an encoding apart
	default:
		return true
	}
}

// exprOrder orders a against b as an aggregation expression compares values: across types by the BSON comparison order, numbers by
// their exact values (NaN below every other number and equal to itself), strings by their bytes, documents by their fields in key
// order, arrays element by element. ok is false when the order of two values is unknown — two values of a type of the encoding that
// is not equal by [evaluator.ScalarEquality] — or the data budget runs out. Every value compared is charged to the data budget.
func (e *evaluation) exprOrder(a, b any) (int, bool) {
	if !e.ChargeComparison(a, b) {
		return 0, false
	}

	enc := e.Encoding()

	ra, rb := exprRank(enc, a), exprRank(enc, b)
	if ra != rb {
		return cmp.Compare(ra, rb), true
	}

	switch ra {
	case rankNull:
		return 0, true
	case rankNumber:
		return exprNumberOrder(a, b)
	case rankDocument:
		return e.exprDocumentOrder(a.(map[string]any), b.(map[string]any)) //nolint:forcetypeassert,errcheck // ranked documents
	case rankArray:
		return e.exprArrayOrder(a.([]any), b.([]any)) //nolint:forcetypeassert,errcheck // ranked arrays
	case rankOther:
		if ext, ok := enc.(evaluator.ScalarEquality); ok && ext.Equal(a, b) {
			return 0, true
		}

		return 0, false
	default:
		return mongoOrder(enc, a, b)
	}
}

// exprNumberOrder orders two numbers exactly; a NaN is below every other number and equal to another NaN.
func exprNumberOrder(a, b any) (int, bool) {
	aNaN, bNaN := isNaN(a), isNaN(b)
	if aNaN || bNaN {
		return cmp.Compare(boolRank(!aNaN), boolRank(!bNaN)), true
	}

	return compareNumbers(a, b)
}

// isNaN reports whether v is a float64 NaN.
func isNaN(v any) bool {
	f, ok := v.(float64)
	return ok && math.IsNaN(f)
}

// exprArrayOrder orders two arrays element by element, a shorter prefix first.
func (e *evaluation) exprArrayOrder(a, b []any) (int, bool) {
	if !e.Charge(min(len(a), len(b))) {
		return 0, false
	}

	for i := range min(len(a), len(b)) {
		c, ok := e.exprOrder(e.Stored(a[i]), e.Stored(b[i]))
		if !ok || c != 0 {
			return c, ok
		}
	}

	return cmp.Compare(len(a), len(b)), true
}

// exprDocumentOrder orders two documents field by field in key order (a Go map keeps no field order): by the field names, then
// by their values, a shorter prefix first. Two documents with the same fields and values are equal.
func (e *evaluation) exprDocumentOrder(a, b map[string]any) (int, bool) {
	if !e.Charge(len(a) + len(b)) {
		return 0, false
	}

	aKeys, bKeys := sortedKeys(a), sortedKeys(b)
	for i := range min(len(aKeys), len(bKeys)) {
		if c := strings.Compare(aKeys[i], bKeys[i]); c != 0 {
			return c, true
		}

		c, ok := e.exprOrder(e.Stored(a[aKeys[i]]), e.Stored(b[bKeys[i]]))
		if !ok || c != 0 {
			return c, ok
		}
	}

	return cmp.Compare(len(aKeys), len(bKeys)), true
}

// sortedKeys returns the keys of m in order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

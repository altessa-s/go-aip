// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"bytes"
	"cmp"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// This file implements [WithSchemaZeroWhenAbsent]: a declared field that a
// document lacks stands for the field's zero value. Every predicate over such
// a field is first built as usual, then made exact for the absent case by
// asking whether MongoDB would match that predicate against a document
// storing the zero value — the two documents decode to the same Go value, so
// they must be selected together. Because each leaf is exact on its own, the
// rewrite composes with !, && and || without any special casing.

// zeroKind returns the kind declared for node by [WithSchemaZeroWhenAbsent],
// or false when node is not a declared field.
func (t *FilterTranslator) zeroKind(node filter.Node) (filter.FieldKind, bool) {
	ident, ok := node.(*filter.IdentNode)
	if !ok {
		return filter.FieldKindUnspecified, false
	}

	return t.semantics.ZeroWhenAbsent(ident.Name)
}

// absentAs makes base match a document whose field matches null (missing or
// BSON null) exactly when zeroMatches. The guard applies to the whole document,
// including a path with both null and non-null candidates through an array.
func absentAs(field string, zeroMatches bool, base bson.M) bson.M {
	if zeroMatches {
		return bson.M{"$or": bson.A{base, bson.M{field: nil}}}
	}

	return bson.M{opAnd: bson.A{base, bson.M{field: bson.M{opNe: nil}}}}
}

// zeroValue returns the value a field of kind holds when it is absent.
func zeroValue(kind filter.FieldKind) any {
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
		return int64(0) // numbers compare by value across BSON types
	default:
		return nil
	}
}

// zeroMatchesComparison reports whether MongoDB's comparison op against
// value matches a stored zero of kind.
//
// It follows MongoDB's query semantics: a comparison only matches within a
// type class (numbers compare by value across int and double), so equality
// and ordering across classes are false and $ne is true. null never equals a
// stored value, and NaN is treated as unordered, so only $ne matches it.
func zeroMatchesComparison(op filter.Operator, kind filter.FieldKind, value any) bool {
	c, ok := compareZero(zeroValue(kind), value)
	if !ok {
		return op == filter.OpNotEqual
	}

	switch op {
	case filter.OpEqual:
		return c == 0
	case filter.OpNotEqual:
		return c != 0
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

// zeroMatchesIn reports whether a stored zero of kind is in values.
func zeroMatchesIn(kind filter.FieldKind, values []any) bool {
	for _, v := range values {
		if zeroMatchesComparison(filter.OpEqual, kind, v) {
			return true
		}
	}

	return false
}

// compareZero orders zero, a value from [zeroValue], against v the way a
// MongoDB query does, reporting false when the two are not comparable.
//
// A nil []byte operand is not comparable: the driver sends it as null.
// A timestamp is compared at millisecond precision, the precision a BSON
// date holds: the driver truncates both the stored value and the literal
// the same way, so a literal within the zero time's millisecond equals it.
func compareZero(zero, v any) (int, bool) {
	switch z := zero.(type) {
	case int64, float64:
		return compareNumber(v)
	case string:
		s, ok := v.(string)
		return strings.Compare(z, s), ok
	case bool:
		b, ok := v.(bool)
		return cmp.Compare(boolRank(z), boolRank(b)), ok
	case []byte:
		// The driver encodes a nil slice as BSON null, never as binary.
		b, ok := v.([]byte)
		return bytes.Compare(z, b), ok && b != nil
	case time.Time:
		ts, ok := v.(time.Time)
		return cmp.Compare(z.UnixMilli(), ts.UnixMilli()), ok
	default:
		return 0, false
	}
}

// compareNumber orders the number zero against v.
func compareNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return cmp.Compare(0, n), true
	case uint64:
		if n == 0 {
			return 0, true
		}

		return -1, true
	case float64:
		if math.IsNaN(n) {
			return 0, false
		}

		return cmp.Compare(0, n), true
	case bson.Decimal128: // a FieldKindDecimal literal as emitted
		if n.IsNaN() {
			return 0, false
		}

		if inf := n.IsInf(); inf != 0 {
			return -inf, true
		}

		coefficient, _, err := n.BigInt()
		if err != nil {
			return 0, false
		}

		return -coefficient.Sign(), true
	default:
		return 0, false
	}
}

// boolRank orders false before true, as MongoDB does.
func boolRank(b bool) int {
	if b {
		return 1
	}

	return 0
}

// zeroMatchesSize reports whether a size() comparison op against value
// matches a stored zero of kind. Only a string has a length — 0 — that the
// emitted forms measure; any other stored value, BSON binary included, has
// none, so equality and ordering fail while != holds.
func zeroMatchesSize(op filter.Operator, kind filter.FieldKind, value any) bool {
	if kind != filter.FieldKindString {
		return op == filter.OpNotEqual
	}

	return zeroMatchesComparison(op, filter.FieldKindInt, value)
}

// zeroMatchesRegex tests the original user pattern against the absent string's zero
// using the configured policy compiler and cache.
func (t *FilterTranslator) zeroMatchesRegex(kind filter.FieldKind, pattern string) (bool, error) {
	if kind != filter.FieldKindString {
		return false, nil
	}

	re, err := t.config.CompileRegex(pattern)
	if err != nil {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "regex %q: %w", pattern, err)
	}

	return re.MatchString(""), nil
}

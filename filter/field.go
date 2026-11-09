// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

// FieldKind is the declared kind of a queryable field, used by translators
// and the in-memory evaluator to reject filter literals whose Go type does
// not align with the field's schema. See [WithFieldTypes].
type FieldKind uint8

const (
	// FieldKindUnspecified disables type-checking for a field. Equivalent to
	// omitting the field from the [WithFieldTypes] map.
	FieldKindUnspecified FieldKind = iota
	// FieldKindInt accepts CEL integer literals (int64, uint64).
	FieldKindInt
	// FieldKindFloat accepts CEL numeric literals (float64, int64, uint64).
	FieldKindFloat
	// FieldKindString accepts CEL string literals.
	FieldKindString
	// FieldKindBool accepts CEL bool literals.
	FieldKindBool
	// FieldKindBytes accepts CEL bytes literals.
	FieldKindBytes
	// FieldKindTimestamp accepts time.Time values produced by timestamp().
	FieldKindTimestamp
	// FieldKindDecimal accepts numeric literals (read from their source spelling) and decimal strings ("0.1") as exact decimals
	// (shopspring decimal.Decimal) for money and other values that must compare exactly; see [DecimalOperand] and [DecimalLiteral]. The in-memory evaluator
	// compares it exactly against decimal.Decimal data values and the MongoDB translator emits Decimal128. PostgreSQL
	// schema-aware translation uses its own Numeric bounds; legacy SQL translators reject the declaration ([Policy.RejectFieldKinds]).
	FieldKindDecimal
)

// String returns the kind's name for use in error messages.
func (k FieldKind) String() string {
	switch k {
	case FieldKindUnspecified:
		return "unspecified"
	case FieldKindInt:
		return "int"
	case FieldKindFloat:
		return "float"
	case FieldKindString:
		return "string"
	case FieldKindBool:
		return "bool"
	case FieldKindBytes:
		return "bytes"
	case FieldKindTimestamp:
		return "timestamp"
	case FieldKindDecimal:
		return "decimal"
	default:
		return "FieldKind(?)"
	}
}

// IsNumeric reports whether k holds numbers: [FieldKindInt], [FieldKindFloat] or [FieldKindDecimal].
func (k FieldKind) IsNumeric() bool {
	return k == FieldKindInt || k == FieldKindFloat || k == FieldKindDecimal
}

// KindsComparable reports whether two operands of declared kinds a and b can compare equal: an unspecified kind is not
// checked, and equal kinds or two numeric kinds compare. Translators reject the other pairs (a string field with an int
// field, say) with [ErrFieldTypeMismatch].
func KindsComparable(a, b FieldKind) bool {
	return a == FieldKindUnspecified || b == FieldKindUnspecified || a == b || (a.IsNumeric() && b.IsNumeric())
}

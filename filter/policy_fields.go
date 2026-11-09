// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"math"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/internal/typename"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FieldKind returns the declared kind for a CEL-side field name, or
// [FieldKindUnspecified] when the field has no declaration.
func (o *Policy) FieldKind(field string) FieldKind {
	if o.fieldTypes == nil {
		return FieldKindUnspecified
	}

	kind, _ := o.fieldTypes.Get(field)

	return kind
}

// RejectFieldKinds fails with [ErrUnsupportedType] when a field is declared with one of kinds by [WithFieldTypes]. A translator calls it
// at construction for the kinds it cannot translate faithfully, so the misconfiguration
// surfaces at process start instead of as silently inexact queries.
func (o *Policy) RejectFieldKinds(kinds ...FieldKind) error {
	if o.fieldTypes == nil {
		return nil
	}

	for field, kind := range o.fieldTypes.All() {
		if slices.Contains(kinds, kind) {
			return coreerrs.Wrapf(ErrUnsupportedType, "field %q: kind %s is not supported by this translator", field, kind)
		}
	}

	return nil
}

// EnumValues returns the declared allowed-value set for a CEL-side field
// name, or (nil, false) when the field has no enum declaration.
func (o *Policy) EnumValues(field string) (*coremaps.ImmutableMap[int64, struct{}], bool) {
	if o.enumValues == nil {
		return nil, false
	}

	return o.enumValues.Get(field)
}

// CheckLiteralKind verifies that the right-hand side AST node of a
// comparison or `in` expression is compatible with the schema declared for
// field: its literal value(s) must match the declared kind ([WithFieldTypes])
// and, for enum fields, be members of the declared value set
// ([WithEnumValues]). Non-literal right-hand sides (e.g. a custom-function
// call that returned a [BinaryOpNode]) are skipped — the check is static and
// limited to what the parser produced as a constant.
//
// CheckLiteralKind operates on one (field, literal) pair. For a generic
// comparison whose operand order is not known up-front, prefer
// [Policy.CheckComparison] — it inspects both sides and
// dispatches to CheckLiteralKind for whichever one is the field.
func (o *Policy) CheckLiteralKind(field string, right Node) error {
	if list, ok := right.(*ListNode); ok {
		for _, element := range list.Elements {
			if err := o.CheckLiteralKind(field, element); err != nil {
				return err
			}
		}

		return nil
	}

	kind := o.FieldKind(field)
	if lit, ok := right.(*LiteralNode); ok && lit.Value != nil {
		if codec := o.valueCodec(kind); codec != nil {
			if err := codec.ValidateLiteral(lit.Value); err != nil {
				return err
			}
		} else if kind == FieldKindDecimal {
			if _, err := o.DecimalOperand(lit, lit.Value); err != nil {
				return err
			}
		} else if err := checkLiteralFieldKind(field, kind, right); err != nil {
			return err
		}
	}

	set, _ := o.EnumValues(field)

	return checkEnumMembership(field, set, right)
}

// checkLiteralFieldKind verifies the literal value(s) on the right-hand side
// are assignable to kind, the kind declared for field via [WithFieldTypes].
// Fields without a declared kind are accepted unconditionally.
func checkLiteralFieldKind(field string, kind FieldKind, right Node) error {
	if kind == FieldKindUnspecified {
		return nil
	}

	switch n := right.(type) {
	case *LiteralNode:
		return checkSingleLiteral(field, kind, kindOperand(kind, n))
	case *ListNode:
		for i, elem := range n.Elements {
			lit, ok := elem.(*LiteralNode)
			if !ok {
				continue
			}

			if lit.Value == nil {
				continue
			}

			if !kindAccepts(kind, kindOperand(kind, lit)) {
				return coreerrs.Wrapf(ErrFieldTypeMismatch,
					"field %q (%s): list element [%d] is %s (want %s)",
					field, kind, i, valueKindName(lit.Value), kind)
			}
		}
	}

	return nil
}

// checkEnumMembership verifies the integer literal(s) on the right-hand side
// are members of set, the value set declared for field via [WithEnumValues].
// Fields without a declared set (nil), nil literals, and non-integer literals
// are accepted — the latter are the province of [checkLiteralFieldKind].
func checkEnumMembership(field string, set *coremaps.ImmutableMap[int64, struct{}], right Node) error {
	if set == nil {
		return nil
	}

	switch n := right.(type) {
	case *LiteralNode:
		return checkEnumLiteral(field, set, n.Value)
	case *ListNode:
		for _, elem := range n.Elements {
			lit, ok := elem.(*LiteralNode)
			if !ok {
				continue
			}

			if err := checkEnumLiteral(field, set, lit.Value); err != nil {
				return err
			}
		}
	}

	return nil
}

// CheckComparison verifies a binary comparison operator against the
// declared field-type schema regardless of operand order. Both
// `status == "x"` and `"x" == status` flow through the same check —
// the symmetric form is required because CEL comparisons are
// commutative in their semantic intent and operators may reorder
// before evaluation.
//
// When neither operand is an IdentNode (literal-on-literal, or
// function-call results on both sides), CheckComparison is a no-op:
// there is no schema-bound field to validate against.
func (o *Policy) CheckComparison(left, right Node) error {
	if ident, ok := left.(*IdentNode); ok {
		return o.CheckLiteralKind(ident.Name, right)
	}

	if ident, ok := right.(*IdentNode); ok {
		return o.CheckLiteralKind(ident.Name, left)
	}

	return nil
}

// kindOperand is the value of lit checked against kind: a [FieldKindDecimal] float literal is checked by its source spelling
// ([LiteralNode.Text]) when it has one.
func kindOperand(kind FieldKind, lit *LiteralNode) any {
	if kind == FieldKindDecimal && lit.Text != "" {
		return lit.Text
	}

	return lit.Value
}

// checkSingleLiteral validates one literal value; nil (CEL null) matches anything.
func checkSingleLiteral(field string, kind FieldKind, value any) error {
	if value == nil {
		return nil
	}

	if kind == FieldKindDecimal {
		if _, err := DecimalLiteral(value); err != nil {
			return coreerrs.Wrapf(ErrFieldTypeMismatch, "field %q (%s): %w", field, kind, err)
		}

		return nil
	}

	if !kindAccepts(kind, value) {
		return coreerrs.Wrapf(ErrFieldTypeMismatch,
			"field %q (%s): value is %s (want %s)",
			field, kind, valueKindName(value), kind)
	}

	return nil
}

// checkEnumLiteral rejects an integer literal that is not in the field's
// declared enum set; nil (CEL null) and non-integer literals are accepted.
func checkEnumLiteral(field string, set *coremaps.ImmutableMap[int64, struct{}], value any) error {
	if v, ok := value.(uint64); ok && v > math.MaxInt64 {
		return coreerrs.Wrapf(ErrEnumValueNotAllowed, "field %q: value %d exceeds the enum's int64 range", field, v)
	}

	v, ok := enumInt64(value)
	if !ok || set.Contains(v) {
		return nil
	}

	return coreerrs.Wrapf(ErrEnumValueNotAllowed,
		"field %q: value %d is not in %v", field, v, slices.Sorted(set.Keys()))
}

// enumInt64 normalizes a CEL integer literal to int64. CEL's parser emits
// int64 for plain integer literals (`5`) and uint64 for the unsigned-suffix
// form (`5u`); both must be enforced against the same int64-keyed set.
// Values outside the int64 range report false; checkEnumLiteral rejects them
// before normalization. Non-integer literals are left to the kind check.
func enumInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}

		return int64(v), true
	default:
		return 0, false
	}
}

// kindAccepts reports whether value's Go type is assignable to kind.
// FieldKindFloat accepts integer literals too — CEL parses whole numbers
// as int64, so `price >= 100` would otherwise be rejected against a
// double field.
func kindAccepts(kind FieldKind, value any) bool {
	switch kind {
	case FieldKindUnspecified:
		// Unreachable in practice — every caller short-circuits on
		// Unspecified before reaching kindAccepts. Kept as an explicit
		// case so the exhaustive-switch linter does not flag the
		// switch, and so a future caller that bypasses the short-circuit
		// inherits the safe accept-all behavior.
		return true
	case FieldKindInt:
		switch value.(type) {
		case int64, uint64:
			return true
		}
	case FieldKindFloat:
		switch value.(type) {
		case float64, int64, uint64:
			return true
		}
	case FieldKindString:
		_, ok := value.(string)
		return ok
	case FieldKindBool:
		_, ok := value.(bool)
		return ok
	case FieldKindBytes:
		_, ok := value.([]byte)
		return ok
	case FieldKindTimestamp:
		_, ok := value.(time.Time)
		return ok
	case FieldKindDecimal:
		_, err := DecimalLiteral(value)
		return err == nil
	}

	return false
}

// valueKindName returns the [FieldKind]-flavored label for a Go value,
// so error messages speak in DSL terms (int / string / bool / …)
// instead of Go's typename (int64 / []byte / time.Time). Falls back to
// the type's name (typename.Of, %T cut short: the value may be data) for
// values that don't map to any FieldKind — the message stays informative
// even for unexpected types.
func valueKindName(value any) string {
	switch value.(type) {
	case int64, uint64:
		return FieldKindInt.String()
	case float64:
		return FieldKindFloat.String()
	case string:
		return FieldKindString.String()
	case bool:
		return FieldKindBool.String()
	case []byte:
		return FieldKindBytes.String()
	case time.Time:
		return FieldKindTimestamp.String()
	case decimal.Decimal:
		return FieldKindDecimal.String()
	default:
		return typename.Of(value)
	}
}

// HasFieldKind reports whether any field declares kind.
func (o *Policy) HasFieldKind(kind FieldKind) bool {
	if o.fieldTypes == nil {
		return false
	}

	for _, declared := range o.fieldTypes.All() {
		if declared == kind {
			return true
		}
	}

	return false
}

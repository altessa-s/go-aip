// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// exprKind describes an operand of an expression comparison for its checks.
type exprKind struct {
	kind    filter.FieldKind
	array   bool
	literal bool
	value   any
}

// exprComparison checks a comparison the MongoDB translator renders as an aggregation expression: every operand is a literal, a
// field, a size() or a substring() of a field; no operand is the null literal; the declared kinds can compare; an array field takes
// no ordering comparison. (A size() meets a literal only in the query form, which checks the size operand.)
func (v *evalValidator) exprComparison(op filter.Operator, left, right filter.Node) error {
	l, err := v.operand(left)
	if err != nil {
		return err
	}

	r, err := v.operand(right)
	if err != nil {
		return err
	}

	if (l.literal && l.value == nil) || (r.literal && r.value == nil) {
		return coreerrs.Wrap(filter.ErrUnsupportedOperation, "comparison of a computed value with null")
	}

	if a, b := l.kind, r.kind; !filter.KindsComparable(a, b) {
		return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "cannot compare %s with %s", a, b)
	}

	switch op {
	case filter.OpEqual, filter.OpNotEqual:
		return nil
	case filter.OpLT, filter.OpLTE, filter.OpGT, filter.OpGTE:
		if l.array || r.array {
			return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordering comparison %v of an array field with a computed value", op)
		}

		return nil
	default:
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}
}

// operand checks an operand of an expression comparison.
func (v *evalValidator) operand(node filter.Node) (exprKind, error) {
	switch n := node.(type) {
	case *filter.LiteralNode:
		if n == nil {
			break
		}

		kind, err := exprLiteralKind(n.Value)

		return exprKind{kind: kind, literal: true, value: n.Value}, err
	case *filter.IdentNode:
		if n == nil {
			break
		}

		if err := v.field(n); err != nil {
			return exprKind{}, err
		}

		return exprKind{kind: v.policy.FieldKind(n.Name), array: v.policy.IsArrayField(n.Name)}, nil
	case *filter.CallNode:
		if n == nil {
			break
		}

		switch n.Op {
		case filter.OpSize:
			if n.Target == nil {
				return exprKind{}, coreerrs.Wrap(filter.ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
			}

			return exprKind{kind: filter.FieldKindInt}, v.field(n.Target)
		case filter.OpSubstring:
			return exprKind{kind: filter.FieldKindString}, v.substring(n)
		default:
			return exprKind{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v as a comparison operand", n.Op)
		}
	}

	return exprKind{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a comparison operand", node)
}

// field checks a field operand: a field reference that is not a call, on the allow-list.
func (v *evalValidator) field(node filter.Node) error {
	if err := filter.CheckFieldReference(node); err != nil {
		return err
	}

	ident, ok := node.(*filter.IdentNode)
	if !ok {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T in field position", node)
	}

	if !v.policy.IsFieldAllowed(ident.Name) {
		return coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", ident.Name)
	}

	return nil
}

// substring checks `field.substring(start[, end])`: the target is a field that is not an array and holds strings, the bounds are
// integer literals with 0 <= start <= end.
func (v *evalValidator) substring(call *filter.CallNode) error {
	ident, ok := call.Target.(*filter.IdentNode)
	if !ok || ident == nil {
		if err := filter.CheckFieldReference(call.Target); err != nil {
			return err
		}

		return coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() of a computed value")
	}

	if v.policy.IsArrayField(ident.Name) {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "substring() of array field %s", ident.Name)
	}

	if kind := v.policy.FieldKind(ident.Name); kind != filter.FieldKindUnspecified && kind != filter.FieldKindString {
		return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "substring() of field %q (%s)", ident.Name, kind)
	}

	if _, _, _, err := substringBounds(call.Args); err != nil {
		return err
	}

	return v.field(ident)
}

// stringExpr checks contains, startsWith or endsWith with a field argument: the target and the argument are fields that are not
// arrays, on the allow-list.
func (v *evalValidator) stringExpr(call *filter.CallNode) error {
	arg, ok := call.Args[0].(*filter.IdentNode)
	if !ok || arg == nil {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a string function argument", call.Args[0])
	}

	if (v.isField(call.Target) && !v.isScalarField(call.Target)) || v.policy.IsArrayField(arg.Name) {
		return coreerrs.Wrap(filter.ErrUnsupportedOperation, "string function of an array field with a field argument")
	}

	if err := v.field(call.Target); err != nil {
		return err
	}

	return v.field(arg)
}

// isField reports whether n is a field.
func (v *evalValidator) isField(n filter.Node) bool {
	ident, ok := n.(*filter.IdentNode)
	return ok && ident != nil
}

// exprLiteralKind is the kind class of a literal operand of an expression comparison; a value of a type the MongoDB translator
// cannot emit fails with [filter.ErrUnsupportedType].
func exprLiteralKind(v any) (filter.FieldKind, error) {
	switch v.(type) {
	case int64, uint64:
		return filter.FieldKindInt, nil
	case float64:
		return filter.FieldKindFloat, nil
	case string:
		return filter.FieldKindString, nil
	case bool:
		return filter.FieldKindBool, nil
	case []byte:
		return filter.FieldKindBytes, nil
	case nil, time.Time, decimal.Decimal:
		return filter.FieldKindUnspecified, nil
	default:
		return filter.FieldKindUnspecified, coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

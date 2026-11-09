// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateComparison handles comparison operators.
func (t *FilterTranslator) translateComparison(op filter.Operator, left, right filter.Node) (bson.M, error) {
	// Two field operands, or a substring(), compare under $expr (expr.go).
	if needsExpr(left, right) {
		return t.translateExprComparison(op, left, right)
	}

	// Check if left side is a size() call
	if call, ok := left.(*filter.CallNode); ok && call.Op == filter.OpSize {
		return t.translateSizeComparison(op, call, right)
	}

	// Type-check first: a mirrored `"lit" == field` is a type mismatch, not
	// a malformed field reference.
	if err := t.config.CheckComparison(left, right); err != nil {
		return nil, err
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return nil, err
	}

	value, err := filter.AcceptLiteral(visitor{t}, right)
	if err != nil {
		return nil, err
	}

	if err = checkOrderedOperand(op, field, t.config.FieldKind(identName(left)), value); err != nil {
		return nil, err
	}

	emitted, err := t.decimalValue(left, right, value)
	if err != nil {
		return nil, err
	}

	if ident, ok := left.(*filter.IdentNode); ok && t.config.PortableContract() && t.config.IsArrayField(ident.Name) && value == nil {
		if op == filter.OpEqual {
			return bson.M{field + ".0": bson.M{opExists: false}}, nil
		}

		if op == filter.OpNotEqual {
			return bson.M{field + ".0": bson.M{opExists: true}}, nil
		}
	}

	if kind, declared := t.zeroKind(left); declared && value == nil && (op == filter.OpEqual || op == filter.OpNotEqual) {
		emitted = zeroValue(kind)
	}

	if ident, ok := left.(*filter.IdentNode); ok && t.semantics.IsPresenceField(ident.Name) && op == filter.OpNotEqual && value != nil {
		return bson.M{field: bson.M{"$exists": true, "$nin": bson.A{emitted, nil}}}, nil
	}

	base, err := t.buildComparisonFilter(field, op, emitted)
	if err != nil {
		return nil, err
	}

	if kind, ok := t.zeroKind(left); ok {
		return absentAs(field, zeroMatchesComparison(op, kind, emitted), base), nil
	}

	return base, nil
}

// checkOrderedOperand rejects the ordered comparisons whose MongoDB meaning departs from CEL's, with
// [filter.ErrUnsupportedOperation]. A null bound orders nothing in CEL, while MongoDB's `$gt: null` matches no document and `$gte: null`
// matches the absent and null ones. BinData orders by length before its bytes, so `data < b"b"` would miss "ab", which CEL — and the
// evaluator and the SQL translators — order lexicographically; equality on bytes is exact and stays supported.
func checkOrderedOperand(op filter.Operator, field string, kind filter.FieldKind, value any) error {
	if op.IsEquality() {
		return nil
	}

	if value == nil {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v of %q with null", op, field)
	}

	if _, isBytes := value.([]byte); isBytes || kind == filter.FieldKindBytes {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v of bytes field %q: MongoDB orders BinData by length first", op, field)
	}

	return nil
}

// identName returns the field name of an identifier node, or "" for any other node.
func identName(n filter.Node) string {
	if ident, ok := n.(*filter.IdentNode); ok {
		return ident.Name
	}

	return ""
}

// isArrayField reports whether node is a field declared by [filter.WithArrayFields].
func (t *FilterTranslator) isArrayField(node filter.Node) bool {
	ident, ok := node.(*filter.IdentNode)
	return ok && t.config.IsArrayField(ident.Name)
}

// isDecimalField reports whether node is a field declared [filter.FieldKindDecimal].
func (t *FilterTranslator) isDecimalField(node filter.Node) bool {
	ident, ok := node.(*filter.IdentNode)
	return ok && t.config.FieldKind(ident.Name) == filter.FieldKindDecimal
}

// decimalValue converts a literal (value, produced by node) compared with a [filter.FieldKindDecimal] field to a BSON Decimal128,
// so MongoDB compares it with the stored Decimal128 exactly instead of through a binary double; a float literal is read from its
// source spelling ([filter.DecimalOperand]). Other fields and null keep the literal.
func (t *FilterTranslator) decimalValue(field, node filter.Node, value any) (any, error) {
	if value == nil || !t.isDecimalField(field) {
		return value, nil
	}

	d, err := t.config.DecimalOperand(node, value)
	if err != nil {
		return nil, err
	}

	d128, ok := bson.ParseDecimal128FromBigInt(d.Coefficient(), int(d.Exponent()))
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "decimal %s does not fit Decimal128", d)
	}

	return d128, nil
}

// buildComparisonFilter creates a MongoDB comparison filter.
func (t *FilterTranslator) buildComparisonFilter(field string, op filter.Operator, value any) (bson.M, error) {
	switch op {
	case filter.OpEqual:
		return bson.M{field: value}, nil
	case filter.OpNotEqual:
		return bson.M{field: bson.M{opNe: value}}, nil
	case filter.OpLT:
		return bson.M{field: bson.M{opLt: value}}, nil
	case filter.OpLTE:
		return bson.M{field: bson.M{opLte: value}}, nil
	case filter.OpGT:
		return bson.M{field: bson.M{opGt: value}}, nil
	case filter.OpGTE:
		return bson.M{field: bson.M{opGte: value}}, nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}
}

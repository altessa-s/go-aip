// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateComparison handles the comparison operators, including the
// `IS NULL` / `IS NOT NULL` forms a null literal calls for. Under negation an
// equality operator is replaced by its opposite (`!(a == b)` is `a != b` in the
// evaluator, also for NULL operands) and an ordered comparison is wrapped in NOT.
func (t *FilterTranslator) translateComparison(op filter.Operator, left, right filter.Node) (string, error) {
	if err := t.config.CheckComparison(left, right); err != nil {
		return "", err
	}

	negated := t.negated
	if negated && op.IsEquality() {
		op, negated = oppositeEquality(op), false
	}

	if isNullLiteral(right) {
		return t.translateNullComparison(op, left)
	}

	if isNullLiteral(left) {
		return t.translateNullComparison(op, right)
	}

	if ident, ok := left.(*filter.IdentNode); ok && t.config.IsArrayField(ident.Name) {
		return t.translateArrayComparison(op, ident, right, negated)
	}

	if ident, ok := right.(*filter.IdentNode); ok && t.config.IsArrayField(ident.Name) {
		return t.translateArrayComparison(Converse(op), ident, left, negated)
	}

	if err := t.checkOperandKinds(left, right); err != nil {
		return "", err
	}

	sqlOp, err := ComparisonOperator(op)
	if err != nil {
		return "", err
	}

	// Operands are rendered left-to-right so that any placeholders they
	// emit line up with the order of t.args.
	lhs, err := t.fieldOperandSQL(left, right)
	if err != nil {
		return "", err
	}

	rhs, err := t.fieldOperandSQL(right, left)
	if err != nil {
		return "", err
	}

	guard, err := t.substringGuards(left, right)
	if err != nil {
		return "", err
	}

	clause := t.scalarComparison(op, sqlOp, left, right, lhs, rhs)
	if negated {
		clause = "NOT (" + clause + ")"
	}

	if guard != "" {
		return "(" + guard + " AND " + clause + ")", nil
	}

	return clause, nil
}

// scalarComparison renders `lhs op rhs` with the evaluator's treatment of a NULL (or absent) field operand: equal to
// NULL and unequal to any value. `x == v` needs nothing more, since an unknown result never matches, but `x != v` must
// hold for a NULL x, and two fields compare with the dialect's null-safe equality ([NullSafeDialect]). An ordered
// comparison, and a size() or substring() operand of a NULL field, are errors in the evaluator, so they stay unknown.
func (t *FilterTranslator) scalarComparison(op filter.Operator, sqlOp string, left, right filter.Node, lhs, rhs string) string {
	_, leftField := left.(*filter.IdentNode)
	_, rightField := right.(*filter.IdentNode)

	if !op.IsEquality() || (!leftField && !rightField) {
		return lhs + " " + sqlOp + " " + rhs
	}

	if leftField && rightField {
		return t.nullSafeEqual(lhs, rhs, op == filter.OpEqual)
	}

	if op == filter.OpEqual {
		return lhs + " " + sqlOp + " " + rhs
	}

	field, other, otherNode := lhs, rhs, right
	if rightField {
		field, other, otherNode = rhs, lhs, left
	}

	if _, literal := otherNode.(*filter.LiteralNode); literal {
		return "(" + lhs + " " + sqlOp + " " + rhs + " OR " + field + " IS NULL)"
	}

	// A size() or substring() of a NULL field is unknown in the evaluator, which must not match through the NULL test.
	return "(" + lhs + " " + sqlOp + " " + rhs + " OR (" + field + " IS NULL AND " + other + " IS NOT NULL))"
}

// nullSafeEqual renders the two-valued comparison of two field operands under which NULL equals NULL and differs from
// every value, through [NullSafeDialect] or, without it, a portable COALESCE over the plain comparison.
func (t *FilterTranslator) nullSafeEqual(lhs, rhs string, equal bool) string {
	if d, ok := t.dialect.(NullSafeDialect); ok {
		return d.NullSafeEqual(lhs, rhs, equal)
	}

	if equal {
		return "COALESCE(" + lhs + " = " + rhs + ", " + lhs + " IS NULL AND " + rhs + " IS NULL)"
	}

	return "COALESCE(" + lhs + " != " + rhs + ", (" + lhs + " IS NULL) != (" + rhs + " IS NULL))"
}

// oppositeEquality returns != for == and == for !=.
func oppositeEquality(op filter.Operator) filter.Operator {
	if op == filter.OpEqual {
		return filter.OpNotEqual
	}

	return filter.OpEqual
}

// translateNullComparison renders `expr IS [NOT] NULL`, or the dialect's
// emptiness test for an array column that cannot hold NULL ([EmptyArrayDialect]).
// size() and substring() never yield null (the evaluator finds them unknown for
// a NULL operand or an out-of-range substring() bound), so `== null` on
// them matches nothing and `!= null` holds exactly where they are defined.
func (t *FilterTranslator) translateNullComparison(op filter.Operator, other filter.Node) (string, error) {
	// The operand is rendered first so that the allow-list rejects a denied field before the operator's shape.
	argsBefore := len(t.args)

	expr, err := t.operandSQL(other)
	if err != nil {
		return "", err
	}

	if op != filter.OpEqual && op != filter.OpNotEqual {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison %v with null", op)
	}

	if t.isArrayField(other) {
		return t.arrayNullTest(expr, op == filter.OpEqual), nil
	}

	_, call := other.(*filter.CallNode)

	switch {
	case op == filter.OpNotEqual && call:
		guard, err := t.substringGuards(other)
		if err != nil || guard == "" {
			return expr + " IS NOT NULL", err
		}

		return "(" + guard + " AND " + expr + " IS NOT NULL)", nil
	case op == filter.OpNotEqual:
		return expr + " IS NOT NULL", nil
	case call:
		t.args = t.args[:argsBefore]
		return MatchNone, nil
	default:
		return expr + " IS NULL", nil
	}
}

// checkOperandKinds rejects two non-literal operands whose declared kinds can never compare equal (a string field
// with an int field, say) with [filter.ErrFieldTypeMismatch], as the MongoDB translator does. Numeric kinds compare
// with each other; an undeclared kind (or a literal, whose kind the policy checks) is accepted.
func (t *FilterTranslator) checkOperandKinds(left, right filter.Node) error {
	a, b := t.operandKind(left), t.operandKind(right)
	if filter.KindsComparable(a, b) {
		return nil
	}

	return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "cannot compare %s with %s", a, b)
}

// operandKind returns the declared kind of a non-literal comparison operand: a field's declared kind, int for size()
// and string for substring(). Literals and anything else are unspecified.
func (t *FilterTranslator) operandKind(node filter.Node) filter.FieldKind {
	switch n := node.(type) {
	case *filter.IdentNode:
		return t.config.FieldKind(n.Name)
	case *filter.CallNode:
		switch n.Op {
		case filter.OpSize:
			return filter.FieldKindInt
		case filter.OpSubstring:
			return filter.FieldKindString
		default:
		}
	}

	return filter.FieldKindUnspecified
}

// Converse returns the operator that keeps a comparison's meaning when its
// operands swap sides: `a < b` is `b > a`. Equality operators are their own
// converse; any other operator is returned unchanged.
func Converse(op filter.Operator) filter.Operator {
	switch op {
	case filter.OpLT:
		return filter.OpGT
	case filter.OpLTE:
		return filter.OpGTE
	case filter.OpGT:
		return filter.OpLT
	case filter.OpGTE:
		return filter.OpLTE
	default:
		return op
	}
}

// ComparisonOperator maps a filter comparison operator to its SQL spelling.
// All six are spelled identically across the supported dialects; any other
// operator fails with [filter.ErrUnsupportedOperation].
func ComparisonOperator(op filter.Operator) (string, error) {
	switch op {
	case filter.OpEqual:
		return "=", nil
	case filter.OpNotEqual:
		return "!=", nil
	case filter.OpLT:
		return "<", nil
	case filter.OpLTE:
		return "<=", nil
	case filter.OpGT:
		return ">", nil
	case filter.OpGTE:
		return ">=", nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}
}

// isNullLiteral reports whether n is the CEL `null` literal.
func isNullLiteral(n filter.Node) bool {
	lit, ok := n.(*filter.LiteralNode)
	return ok && lit.Value == nil
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
)

// renderComparison renders a comparison other than `in` with the default evaluator's NULL semantics: a null value equals null
// and differs from every other value, so `x != v` holds for a NULL column x and two nullable columns compare null-safely, while
// an ordered comparison with a NULL operand is unknown, as is any comparison with a NULL computed operand.
//
//nolint:gocognit,gocyclo // one case per operand shape
func (r *renderer) renderComparison(e *expression, negated bool) (string, error) {
	left, right := e.Children[0], e.Children[1]

	op := e.Op
	if negated && op.IsEquality() {
		op, negated = oppositeEquality(op), false
	}

	if left.IsNull() || right.IsNull() {
		return r.renderNullComparison(op, left, right)
	}

	a, err := r.render(left)
	if err != nil {
		return "", err
	}

	c, err := r.render(right)
	if err != nil {
		return "", err
	}

	if left.Array && !right.Array {
		return r.renderElementComparison(op, right, a, c, negated), nil
	}

	if left.Array && right.Array && left.Kind != right.Kind {
		kind := sqltype.ComparisonKind(left.Kind, right.Kind)
		if left.Kind != kind {
			a = "(" + a + ")::" + typeName(kind) + "[]"
		}

		if right.Kind != kind {
			c = "(" + c + ")::" + typeName(kind) + "[]"
		}
	}

	sql := "(" + a + " " + sqlOperator(op) + " " + c + ")"
	if !op.IsEquality() {
		if negated {
			return "(NOT " + sql + ")", nil
		}

		return sql, nil
	}

	leftNullable, rightNullable := nullable(left, r.contract), nullable(right, r.contract)

	switch {
	case leftNullable && rightNullable:
		if op == filter.OpEqual {
			return "(" + a + " IS NOT DISTINCT FROM " + c + ")", nil
		}

		return "(" + a + " IS DISTINCT FROM " + c + ")", nil
	case op == filter.OpEqual || (!leftNullable && !rightNullable):
		return sql, nil
	}

	field, other, otherExpr := a, c, right
	if rightNullable {
		field, other, otherExpr = c, a, left
	}

	if computed(otherExpr) {
		// A NULL computed operand is unknown and must not match through the NULL test.
		return "(" + a + " <> " + c + " OR (" + field + " IS NULL AND " + other + " IS NOT NULL))", nil
	}

	return "(" + a + " <> " + c + " OR " + field + " IS NULL)", nil
}

// renderElementComparison renders `array op scalar` (a and c rendered): != holds when no element equals the scalar, every other
// operator when some element satisfies it, negated its complement. A NULL array has no element and a NULL element satisfies
// nothing, so a negated test holds for both. A NULL scalar is unknown, except that a null value (a NULL column) satisfies nothing
// in an empty array: see [arrayPredicate].
func (r *renderer) renderElementComparison(op filter.Operator, right *expression, a, c string, negated bool) string {
	match, holds := op, !negated
	if op == filter.OpNotEqual {
		match, holds = filter.OpEqual, negated
	}

	sql := "(" + c + " " + reverseOperator(match) + " ANY(" + a + "))"
	if holds {
		return sql
	}

	return r.noElement(sql, right, c, a)
}

// noElement renders the negation of member, a test that some element of the array arr equals or compares with the scalar
// operand rendered as operand: TRUE where no element does, also for a NULL array or NULL elements. A NULL operand is unknown,
// except that a null value (a NULL column or the null literal) is in no empty array.
func (r *renderer) noElement(member string, operandExpr *expression, operand, arr string) string {
	none := "NOT COALESCE(" + member + ", FALSE)"

	switch {
	case operandExpr.Tag == filter.NodeKindLiteral && !operandExpr.IsNull():
		return "(" + none + ")"
	case computed(operandExpr) || !nullSafe(operandExpr, r.contract):
		return "((" + operand + ") IS NOT NULL AND " + none + ")"
	}

	empty := "COALESCE(cardinality(" + arr + "), 0) = 0"
	if r.contract {
		empty = "cardinality(" + arr + ") = 0"
	}

	return "(" + none + " AND ((" + operand + ") IS NOT NULL OR " + empty + "))"
}

// renderNullComparison renders `x == null` or `x != null`, never unknown: an IS [NOT] NULL test, the zero-value test of a NOT
// NULL column of the listing contract, and for a computed operand (size(), a predicate) `== null` never holds, as the default
// evaluator finds its NULL unknown, and `!= null` holds where it is defined.
func (r *renderer) renderNullComparison(op filter.Operator, left, right *expression) (string, error) {
	field := left
	if left.IsNull() {
		field = right
	}

	operand, err := r.render(field)
	if err != nil {
		return "", err
	}

	if r.contract && field.Tag == filter.NodeKindIdent && !nullable(field, true) {
		return r.renderZeroComparison(op, field, operand)
	}

	switch {
	case computed(field) && op == filter.OpEqual:
		return sqlFalse, nil
	case op == filter.OpEqual:
		return "((" + operand + ") IS NULL)", nil
	default:
		return "((" + operand + ") IS NOT NULL)", nil
	}
}

// renderZeroComparison renders `field == null` or `field != null` on a NOT NULL column of the listing contract, where null is
// the zero value and an empty array.
func (r *renderer) renderZeroComparison(op filter.Operator, field *expression, col string) (string, error) {
	if field.Array {
		if op == filter.OpEqual {
			return "(cardinality(" + col + ") = 0)", nil
		}

		return "(cardinality(" + col + ") > 0)", nil
	}

	if field.Kind == sqlbase.UUID {
		if op == filter.OpEqual {
			return sqlFalse, nil
		}

		return sqlTrue, nil
	}

	v, err := r.literal(zeroValue(field.Kind), field.Kind)
	if err != nil {
		return "", err
	}

	return "(" + col + " " + sqlOperator(op) + " " + v + ")", nil
}

// oppositeEquality returns != for == and == for !=.
func oppositeEquality(op filter.Operator) filter.Operator {
	if op == filter.OpEqual {
		return filter.OpNotEqual
	}

	return filter.OpEqual
}

func zeroValue(k sqlbase.Kind) any {
	switch k {
	case sqlbase.Boolean:
		return false
	case sqlbase.Integer, sqlbase.Numeric:
		return int64(0)
	case sqlbase.Float:
		return float64(0)
	case sqlbase.Text, sqlbase.UUID:
		return ""
	case sqlbase.Bytes:
		return []byte{}
	case sqlbase.Timestamp:
		return time.Time{}
	default:
		return nil
	}
}

func sqlOperator(op filter.Operator) string {
	switch op {
	case filter.OpEqual:
		return "="
	case filter.OpNotEqual:
		return "<>"
	case filter.OpLT:
		return "<"
	case filter.OpLTE:
		return "<="
	case filter.OpGT:
		return ">"
	case filter.OpGTE:
		return ">="
	default:
	}

	return ""
}

func reverseOperator(op filter.Operator) string {
	switch op {
	case filter.OpLT:
		return ">"
	case filter.OpLTE:
		return ">="
	case filter.OpGT:
		return "<"
	case filter.OpGTE:
		return "<="
	default:
	}

	return sqlOperator(op)
}

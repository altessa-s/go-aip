// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"github.com/altessa-s/go-aip/filter"
)

// negate renders `!node`. Meilisearch filters are two-valued: a predicate on a null or absent attribute is false, and NOT
// selects every document its operand does not, null and absent attributes included. The evaluator applies SQL's
// three-valued logic instead, under which an ordered comparison, a bare boolean or size() of a null value is unknown and
// stays unknown under !. Wrapping such a predicate in NOT would select the documents the evaluator leaves out, so ! is
// pushed down through && and || (De Morgan) to each predicate, which renders its own negation:
//
//   - `!(a == v)` / `!(a != v)` is the opposite equality, null forms included;
//   - `!(a < v)` (any ordered comparison of a scalar field) is the opposite ordering `a >= v`, false for a null or absent
//     attribute as the evaluator's unknown is unmatched;
//   - `!a` (bare boolean) is `a = false`, for the same reason;
//   - `!(size(a) == 0)` (an emptiness test) is the opposite emptiness test;
//   - every other predicate — `in`, has(), string functions, array-field tests, application predicates — is false, not
//     unknown, in the evaluator for a null or absent value, so its negation is NOT (…), which selects that value.
func (t *FilterTranslator) negate(node filter.Node) (string, error) {
	if t.predicateRenderer != nil {
		out, handled, err := t.predicateRenderer(node)
		if err != nil {
			return "", err
		}

		if handled {
			return "NOT (" + out + ")", nil
		}
	}

	switch n := node.(type) {
	case *filter.IdentNode:
		field, err := t.getFieldName(n)
		if err != nil {
			return "", err
		}

		return field + " = false", nil
	case *filter.UnaryOpNode:
		if n.Op == filter.OpNot {
			if err := t.depth.Enter(); err != nil {
				return "", err
			}
			defer t.depth.Leave()

			return t.acceptString(n.Operand)
		}
	case *filter.BinaryOpNode:
		return t.negateBinary(n)
	}

	inner, err := t.acceptString(node)
	if err != nil {
		return "", err
	}

	return "NOT (" + inner + ")", nil
}

// negateBinary renders the negation of a binary operation: De Morgan for && and ||, NOT (…) for `in`, the negated
// comparison otherwise.
func (t *FilterTranslator) negateBinary(n *filter.BinaryOpNode) (string, error) {
	if err := t.depth.Enter(); err != nil {
		return "", err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpAnd:
		return t.joinLogical("OR", n.Left, n.Right, t.negate)
	case filter.OpOr:
		return t.joinLogical("AND", n.Left, n.Right, t.negate)
	case filter.OpIn:
		inner, err := t.translateIn(n.Left, n.Right)
		if err != nil {
			return "", err
		}

		return "NOT (" + inner + ")", nil
	default:
		return t.negateComparison(n.Op, n.Left, n.Right)
	}
}

// negateComparison renders `!(left op right)`. An ordered comparison of an array field tests whether some element
// satisfies it, which the evaluator finds false, not unknown, for an absent array, so its negation stays NOT (…).
func (t *FilterTranslator) negateComparison(op filter.Operator, left, right filter.Node) (string, error) {
	if isSizeCall(left) || isSizeCall(right) {
		return t.translateSizeComparison(op, left, right, true)
	}

	if !op.IsEquality() && (t.isArrayField(left) || t.isArrayField(right)) {
		inner, err := t.translateComparison(op, left, right)
		if err != nil {
			return "", err
		}

		return "NOT (" + inner + ")", nil
	}

	return t.translateComparison(oppositeComparison(op), left, right)
}

// oppositeComparison returns the comparison operator that holds exactly where op does not, on two values of one
// ordered type.
func oppositeComparison(op filter.Operator) filter.Operator {
	switch op {
	case filter.OpEqual:
		return filter.OpNotEqual
	case filter.OpNotEqual:
		return filter.OpEqual
	case filter.OpLT:
		return filter.OpGTE
	case filter.OpLTE:
		return filter.OpGT
	case filter.OpGT:
		return filter.OpLTE
	case filter.OpGTE:
		return filter.OpLT
	default:
		return op
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// isArrayField reports whether node names a field declared by [filter.WithArrayFields].
func (t *FilterTranslator) isArrayField(node filter.Node) bool {
	ident, ok := node.(*filter.IdentNode)
	return ok && t.config.IsArrayField(ident.Name)
}

// arrayDialect returns the dialect's array support or [filter.ErrUnsupportedOperation].
func (t *FilterTranslator) arrayDialect() (ArrayDialect, error) {
	arrays, ok := t.dialect.(ArrayDialect)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "array fields are not supported by this dialect")
	}

	return arrays, nil
}

// arrayNullTest renders the test that the array column col holds no array (isNull) or holds one. A column that can
// hold NULL uses IS [NOT] NULL; a dialect whose array columns cannot ([EmptyArrayDialect]) stores an absent array as an
// empty one, so the test becomes emptiness.
func (t *FilterTranslator) arrayNullTest(col string, isNull bool) string {
	if empty, ok := t.dialect.(EmptyArrayDialect); ok {
		return empty.ArrayEmpty(col, isNull)
	}

	if isNull {
		return col + " IS NULL"
	}

	return col + " IS NOT NULL"
}

// arrayNegation renders the negation of clause, a predicate over the elements of the array column col. The evaluator
// finds no element in an absent array, so the negation holds for it; under SQL three-valued logic NOT (...) is unknown
// on a NULL array, so a nullable array column gets an explicit NULL test. Array columns that cannot hold NULL
// ([EmptyArrayDialect]) need none.
func (t *FilterTranslator) arrayNegation(col, clause string) string {
	if _, nonNullable := t.dialect.(EmptyArrayDialect); nonNullable {
		return "NOT (" + clause + ")"
	}

	return "(" + col + " IS NULL OR NOT (" + clause + "))"
}

// translateArrayComparison renders `field op other` for an array field with element semantics: != holds when no
// element equals the operand, also for a NULL (absent) array, every other operator when some element satisfies it;
// negated renders the negation of an ordered comparison. The operand must be a literal.
func (t *FilterTranslator) translateArrayComparison(op filter.Operator, field *filter.IdentNode, other filter.Node, negated bool) (string, error) {
	arrays, err := t.arrayDialect()
	if err != nil {
		return "", err
	}

	lit, ok := other.(*filter.LiteralNode)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "array field %s compared with %T", field.Name, other)
	}

	if _, err = ComparisonOperator(op); err != nil {
		return "", err
	}

	col, err := t.column(field)
	if err != nil {
		return "", err
	}

	operand, err := t.visitLiteral(lit)
	if err != nil {
		return "", err
	}

	kind := t.config.FieldKind(field.Name)
	if kind == filter.FieldKindDecimal {
		if operand, err = t.config.DecimalOperand(lit, lit.Value); err != nil {
			return "", err
		}
	}

	if op == filter.OpNotEqual {
		op, negated = filter.OpEqual, true
	}

	clause, err := arrays.ArrayCompare(col, op, operand, kind, t.renderValue)
	if err != nil || !negated {
		return clause, err
	}

	return t.arrayNegation(col, clause), nil
}

// translateArrayIn renders `field in [a, b]` for an array field: some element equals one of the list values. Under
// negation each equality is negated and the disjunction becomes a conjunction.
func (t *FilterTranslator) translateArrayIn(field *filter.IdentNode, list *filter.ListNode) (string, error) {
	if _, err := t.column(field); err != nil {
		return "", err
	}

	if len(list.Elements) == 0 {
		if t.negated {
			return MatchAll, nil
		}

		return MatchNone, nil
	}

	parts := make([]string, 0, len(list.Elements))

	for _, item := range list.Elements {
		part, err := t.translateComparison(filter.OpEqual, field, item)
		if err != nil {
			return "", err
		}

		parts = append(parts, "("+part+")")
	}

	if len(parts) == 1 {
		return parts[0], nil
	}

	return "(" + strings.Join(parts, " "+t.connective("OR")+" ") + ")", nil
}

// translateArrayStringPredicate renders a string predicate over the elements of an array field.
func (t *FilterTranslator) translateArrayStringPredicate(n *filter.CallNode, col string) (string, error) {
	arrays, err := t.arrayDialect()
	if err != nil {
		return "", err
	}

	if filter.IsFieldReference(n.Args[0]) {
		return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "string function over an array field with a field argument")
	}

	needle, err := t.stringNeedle(n)
	if err != nil {
		return "", err
	}

	return arrays.ArrayStringPredicate(n.Op, col, needle, t.value)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateLogical handles the && and || operators. Under negation the operands are negated and the connective swapped
// (De Morgan), which holds under the evaluator's three-valued logic as it does under SQL's.
func (t *FilterTranslator) translateLogical(sqlOp string, left, right filter.Node) (string, error) {
	lhs, err := t.acceptPredicate(left)
	if err != nil {
		return "", err
	}

	rhs, err := t.acceptPredicate(right)
	if err != nil {
		return "", err
	}

	return "(" + lhs + ") " + t.connective(sqlOp) + " (" + rhs + ")", nil
}

// connective returns the SQL connective sqlOp ("AND" or "OR"), swapped under negation.
func (t *FilterTranslator) connective(sqlOp string) string {
	if !t.negated {
		return sqlOp
	}

	if sqlOp == "AND" {
		return "OR"
	}

	return "AND"
}

// translateIn handles the in operator.
func (t *FilterTranslator) translateIn(left, right filter.Node) (string, error) {
	// `x in tags` on an array field is the membership test `tags == x`.
	if field, ok := right.(*filter.IdentNode); ok && t.config.IsArrayField(field.Name) {
		if filter.IsFieldReference(left) {
			return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "field operand in an array field")
		}

		return t.translateComparison(filter.OpEqual, field, left)
	}

	if ident, ok := left.(*filter.IdentNode); ok {
		if err := t.config.CheckLiteralKind(ident.Name, right); err != nil {
			return "", err
		}
	}

	list, ok := right.(*filter.ListNode)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "in operator requires a list, got %T", right)
	}

	if ident, ok := left.(*filter.IdentNode); ok && t.config.IsArrayField(ident.Name) {
		return t.translateArrayIn(ident, list)
	}

	if call, ok := left.(*filter.CallNode); ok && call.Op == filter.OpSubstring && !t.inSubstring {
		return t.translateSubstringIn(left, list)
	}

	// The left operand is rendered even for an empty list so that the
	// allow-list and identifier checks still run on it; the placeholders
	// it may have emitted are rolled back before the constant is
	// returned, keeping numbered dialects consistent.
	argsBefore := len(t.args)

	expr, err := t.operandSQL(left)
	if err != nil {
		return "", err
	}

	if len(list.Elements) == 0 {
		clause := t.emptyIn(left, expr)
		if clause == MatchNone || clause == MatchAll {
			t.args = t.args[:argsBefore]
		}

		return clause, nil
	}

	for _, elem := range list.Elements {
		if _, literal := elem.(*filter.LiteralNode); !literal {
			// ClickHouse requires a constant IN set. Equality disjunctions
			// preserve dynamic operands on every dialect. Render the left
			// operand for each branch to keep positional bind slots aligned.
			t.args = t.args[:argsBefore]

			parts := make([]string, 0, len(list.Elements))
			for _, item := range list.Elements {
				part, err := t.translateComparison(filter.OpEqual, left, item)
				if err != nil {
					return "", err
				}

				parts = append(parts, "("+part+")")
			}

			return "(" + strings.Join(parts, " "+t.connective("OR")+" ") + ")", nil
		}
	}

	return t.translateLiteralIn(left, expr, list)
}

// emptyIn renders `left in []`, which matches nothing. Its negation matches every row where left, already rendered as
// expr, is defined: always for a field or a literal, where its field is not NULL for size() and substring() (the
// evaluator finds them unknown for a NULL operand).
func (t *FilterTranslator) emptyIn(left filter.Node, expr string) string {
	if !t.negated {
		return MatchNone
	}

	if _, call := left.(*filter.CallNode); !call {
		return MatchAll
	}

	return expr + " IS NOT NULL"
}

// translateLiteralIn renders `left IN (...)` over a list of literals whose left operand already rendered as expr. A
// null element matches a NULL field (or literal), which IN never does under SQL three-valued logic, so it becomes an `IS NULL`
// disjunct: `(expr IN (...) OR expr IS NULL)`, or `expr IS NULL` alone for a list of only nulls. size() and
// substring() never yield null, so for them a null element matches nothing.
//
// Under negation a NULL field is not in a list without a null element, so the negation is
// `(expr NOT IN (...) OR expr IS NULL)`; with a null element it is `expr NOT IN (...)`, or `expr IS NOT NULL` for a
// list of only nulls.
func (t *FilterTranslator) translateLiteralIn(left filter.Node, expr string, list *filter.ListNode) (string, error) {
	rendered := make([]string, 0, len(list.Elements))
	hasNull := false

	for _, elem := range list.Elements {
		if isNullLiteral(elem) {
			hasNull = true
			continue
		}

		sql, err := t.fieldOperandSQL(elem, left)
		if err != nil {
			return "", err
		}

		rendered = append(rendered, sql)
	}

	_, call := left.(*filter.CallNode)
	in := " IN ("

	if t.negated {
		in = " NOT IN ("
	}

	switch {
	case len(rendered) == 0 && t.negated:
		return expr + " IS NOT NULL", nil
	case len(rendered) == 0 && !call:
		return expr + " IS NULL", nil
	case len(rendered) == 0:
		return MatchNone, nil
	case call || hasNull == t.negated:
		return expr + in + strings.Join(rendered, ", ") + ")", nil
	}

	// The left operand is rendered again rather than reused, so that a value it binds is bound once per occurrence
	// in placeholder order.
	again, err := t.operandSQL(left)
	if err != nil {
		return "", err
	}

	return "(" + expr + in + strings.Join(rendered, ", ") + ") OR " + again + " IS NULL)", nil
}

// translateStringPredicate handles contains, startsWith, endsWith and
// matches. The evaluator finds no match in a NULL (or absent) target, so the
// negation holds for it: `(NOT (pred) OR col IS NULL)`, or the array negation
// for an array target. A NULL field needle is unknown in the evaluator and
// stays unknown.
func (t *FilterTranslator) translateStringPredicate(n *filter.CallNode) (string, error) {
	clause, err := t.stringPredicate(n)
	if err != nil || !t.negated {
		return clause, err
	}

	col, err := t.column(n.Target)
	if err != nil {
		return "", err
	}

	if t.isArrayField(n.Target) {
		return t.arrayNegation(col, clause), nil
	}

	return "(NOT (" + clause + ") OR " + col + " IS NULL)", nil
}

// stringPredicate renders a string predicate. The needle is passed to the
// dialect raw, together with [FilterTranslator.value], so the dialect can bind it as
// many times as its rendering needs.
func (t *FilterTranslator) stringPredicate(n *filter.CallNode) (string, error) {
	col, err := t.column(n.Target)
	if err != nil {
		return "", err
	}

	if len(n.Args) != 1 {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "string function requires exactly 1 argument")
	}

	if t.isArrayField(n.Target) {
		return t.translateArrayStringPredicate(n, col)
	}

	if _, ok := n.Args[0].(*filter.IdentNode); ok {
		if n.Op == filter.OpMatches {
			return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "matches() requires a literal pattern for validation")
		}

		needle, columnErr := t.column(n.Args[0])
		if columnErr != nil {
			return "", columnErr
		}
		// A column needs no bind slot, even when endsWith uses it repeatedly.
		return t.dialect.StringPredicate(n.Op, col, "", func(any) (string, error) { return needle, nil })
	}

	needle, err := t.stringNeedle(n)
	if err != nil {
		return "", err
	}

	return t.dialect.StringPredicate(n.Op, col, needle, t.value)
}

// stringNeedle validates the literal argument of a string predicate, including the regex policy for matches().
func (t *FilterTranslator) stringNeedle(n *filter.CallNode) (string, error) {
	arg, err := filter.AcceptLiteral(visitor{t}, n.Args[0])
	if err != nil {
		return "", err
	}

	needle, ok := arg.(string)
	if !ok {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "string function argument must be a string")
	}

	if n.Op == filter.OpMatches {
		if err = t.validateRegex(needle); err != nil {
			return "", err
		}
	}

	return needle, nil
}

// translateSubstringIn renders `field.substring(...) in [...]` behind the substring row guard.
func (t *FilterTranslator) translateSubstringIn(left filter.Node, list *filter.ListNode) (string, error) {
	guard, err := t.substringGuards(left)
	if err != nil {
		return "", err
	}

	t.inSubstring = true
	clause, err := t.translateIn(left, list)
	t.inSubstring = false

	if err != nil || guard == "" || clause == MatchNone {
		return clause, err
	}

	return "(" + guard + " AND " + clause + ")", nil
}

// translateExists handles has() / exists(). A SQL row always carries
// every column of its table, so the closest available meaning is "the
// nullable column holds a value" (for an array column that cannot hold NULL,
// [EmptyArrayDialect], that it is not empty). The test is never unknown, so
// its negation is the opposite test.
func (t *FilterTranslator) translateExists(target filter.Node) (string, error) {
	col, err := t.column(target)
	if err != nil {
		return "", err
	}

	if t.isArrayField(target) {
		return t.arrayNullTest(col, t.negated), nil
	}

	if t.negated {
		return col + " IS NULL", nil
	}

	return col + " IS NOT NULL", nil
}

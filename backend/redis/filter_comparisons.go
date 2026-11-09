// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateComparison handles comparison operators (==, !=, <, >, <=, >=).
func (t *FilterTranslator) translateComparison(op filter.Operator, left, right filter.Node) (string, error) {
	// Type-check first: a mirrored `"lit" == field` is a type mismatch, not
	// a malformed field reference.
	if err := t.config.CheckComparison(left, right); err != nil {
		return "", err
	}

	// CEL null asks whether the field has a value. RediSearch indexes no null
	// value: the tag or term "null" is an ordinary string, so the comparison
	// is the presence test ismissing() answers, or nothing at all.
	if isNullLiteral(right) {
		return t.translateNullComparison(op, left)
	}

	if isNullLiteral(left) {
		return t.translateNullComparison(op, right)
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return "", err
	}

	right, err = t.config.NormalizeDecimalOperand(left, right)
	if err != nil {
		return "", err
	}

	value, err := filter.AcceptLiteral(visitor{t}, right)
	if err != nil {
		return "", err
	}

	// A literal encoder may itself produce nil; it has no query form either.
	if value == nil {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison of %q with an encoded null", field)
	}

	ft := t.fieldType(field)

	return t.buildComparison(field, op, value, ft)
}

// translateNullComparison renders `field == null` as `ismissing(@field)` and
// `field != null` as `-ismissing(@field)`; other operators have no meaning
// against null.
func (t *FilterTranslator) translateNullComparison(op filter.Operator, other filter.Node) (string, error) {
	switch op {
	case filter.OpEqual:
		return t.missingClause(other)
	case filter.OpNotEqual:
		return t.translateExists(other)
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison %v with null", op)
	}
}

// isNullLiteral reports whether n is the CEL `null` literal.
func isNullLiteral(n filter.Node) bool {
	lit, ok := n.(*filter.LiteralNode)
	return ok && lit.Value == nil
}

// buildComparison creates a RediSearch comparison expression.
func (t *FilterTranslator) buildComparison(field string, op filter.Operator, value any, ft FieldType) (string, error) {
	switch ft {
	case FieldTypeNumeric:
		return t.buildNumericComparison(field, op, value)
	case FieldTypeTag:
		return t.buildTagComparison(field, op, value)
	case FieldTypeText:
		return t.buildTextComparison(field, op, value)
	default:
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "unknown field type for %q", field)
	}
}

// buildNumericComparison creates a RediSearch NUMERIC range expression.
func (t *FilterTranslator) buildNumericComparison(field string, op filter.Operator, value any) (string, error) {
	v, err := formatNumericValue(value)
	if err != nil {
		return "", err
	}

	switch op {
	case filter.OpEqual:
		return fmt.Sprintf("%s:[%s %s]", queryField(field), v, v), nil
	case filter.OpNotEqual:
		return fmt.Sprintf("-%s:[%s %s]", queryField(field), v, v), nil
	case filter.OpLT:
		return fmt.Sprintf("%s:[-inf (%s]", queryField(field), v), nil
	case filter.OpLTE:
		return fmt.Sprintf("%s:[-inf %s]", queryField(field), v), nil
	case filter.OpGT:
		return fmt.Sprintf("%s:[(%s +inf]", queryField(field), v), nil
	case filter.OpGTE:
		return fmt.Sprintf("%s:[%s +inf]", queryField(field), v), nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "numeric comparison with operator %v", op)
	}
}

// buildTagComparison creates a RediSearch TAG expression.
func (t *FilterTranslator) buildTagComparison(field string, op filter.Operator, value any) (string, error) {
	v, err := t.termValue(field, value)
	if err != nil {
		return "", err
	}

	switch op {
	case filter.OpEqual:
		return fmt.Sprintf("%s:{%s}", queryField(field), v), nil
	case filter.OpNotEqual:
		return fmt.Sprintf("-%s:{%s}", queryField(field), v), nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "TAG field %q does not support operator %v", field, op)
	}
}

// buildTextComparison creates a RediSearch TEXT expression (== only for exact match via TAG fallback).
func (t *FilterTranslator) buildTextComparison(field string, op filter.Operator, value any) (string, error) {
	v, err := t.termValue(field, value)
	if err != nil {
		return "", err
	}

	// The empty TEXT value is the bare quoted term: `@f:("")` is not the documented form.
	clause := fmt.Sprintf("%s:(%s)", queryField(field), v)
	if v == emptyTerm {
		clause = queryField(field) + ":" + emptyTerm
	}

	switch op {
	case filter.OpEqual:
		return clause, nil
	case filter.OpNotEqual:
		return "-" + clause, nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "TEXT field %q does not support operator %v", field, op)
	}
}

// emptyTerm is the RediSearch query term for the empty string on a field declared INDEXEMPTY.
const emptyTerm = `""`

// termValue renders a TAG or TEXT comparison value: the escaped term, or [emptyTerm] for the empty string. RediSearch has no other
// spelling of an empty value — `@f:{}` is a syntax error and an empty TEXT term matches nothing — and even `@f:{""}` needs the
// field declared INDEXEMPTY ([WithSchemaIndexEmpty]), so the empty string on any other field fails with
// [filter.ErrUnsupportedOperation].
func (t *FilterTranslator) termValue(field string, value any) (string, error) {
	s := fmt.Sprintf("%v", value)
	if s != "" {
		return t.escapeQueryValue(s), nil
	}

	if _, ok := t.indexEmpty[field]; !ok {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation,
			"empty value on field %q needs it declared INDEXEMPTY (WithSchemaIndexEmpty)", field)
	}

	return emptyTerm, nil
}

// translateLogicalAnd handles the && operator. RediSearch uses space for AND.
func (t *FilterTranslator) translateLogicalAnd(left, right filter.Node) (string, error) {
	l, r, err := t.logicalOperands(left, right)
	if err != nil {
		return "", err
	}

	return "(" + l + " " + r + ")", nil
}

// translateLogicalOr handles the || operator. RediSearch uses | for OR.
//
// The union is grouped as a whole, not only its operands: RediSearch binds
// an intersection tighter than a union, so an unwrapped `(a)|(b)` placed next
// to another clause — `x (a)|(b)` from `x && (a || b)` — parses as
// `(x a)|(b)`.
func (t *FilterTranslator) translateLogicalOr(left, right filter.Node) (string, error) {
	l, r, err := t.logicalOperands(left, right)
	if err != nil {
		return "", err
	}

	return "((" + l + ")|(" + r + "))", nil
}

// logicalOperands renders both sides of a logical operator.
//
// This is the part the two spellings share; the joining stays at each
// call site, as concatenation rather than a format template. Threading
// the shape through fmt.Sprintf instead measured 15% slower with six
// extra allocations on the Complex benchmark — too much to pay on the
// hot path for collapsing four lines.
func (t *FilterTranslator) logicalOperands(left, right filter.Node) (string, string, error) {
	l, err := t.acceptPredicate(left)
	if err != nil {
		return "", "", err
	}

	r, err := t.acceptPredicate(right)
	if err != nil {
		return "", "", err
	}

	return l, r, nil
}

// translateNot handles the ! operator.
func (t *FilterTranslator) translateNot(operand filter.Node) (string, error) {
	// For simple identifiers (like !active), treat as field == false TAG
	if ident, ok := operand.(*filter.IdentNode); ok {
		field, err := t.getFieldName(ident)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("-%s:{true}", queryField(field)), nil
	}

	inner, err := t.acceptPredicate(operand)
	if err != nil {
		return "", err
	}

	return "-(" + inner + ")", nil
}

// translateIn handles the in operator.
func (t *FilterTranslator) translateIn(left, right filter.Node) (string, error) {
	// `x in tags` on an array field is a membership test RediSearch answers
	// natively: a TAG, TEXT or NUMERIC attribute indexed from an array matches
	// a document when any element matches, so it is the equality `tags == x`.
	// The literal is checked against the declared element kind.
	if ident, ok := right.(*filter.IdentNode); ok && t.config.IsArrayField(ident.Name) && !filter.IsFieldReference(left) {
		if isNullLiteral(left) {
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "null element test on array field %q", ident.Name)
		}

		return t.translateComparison(filter.OpEqual, right, left)
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return "", err
	}

	if ident, ok := left.(*filter.IdentNode); ok {
		if err = t.config.CheckLiteralKind(ident.Name, right); err != nil {
			return "", err
		}
	}

	right, err = t.config.NormalizeDecimalOperand(left, right)
	if err != nil {
		return "", err
	}

	values, err := right.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	valuesSlice, ok := values.([]any)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "in operator requires a list, got %T", values)
	}

	// A null element matches an absent value: the membership test of the other elements, or ismissing(@field) when the field is
	// declared INDEXMISSING. Rendered as an element it would be the literal tag or term "null".
	if !slices.Contains(valuesSlice, nil) {
		return t.buildIn(field, valuesSlice, t.fieldType(field))
	}

	missing, err := t.missingClause(left)
	if err != nil {
		return "", err
	}

	valuesSlice = slices.DeleteFunc(valuesSlice, func(v any) bool { return v == nil })
	if len(valuesSlice) == 0 {
		return missing, nil
	}

	in, err := t.buildIn(field, valuesSlice, t.fieldType(field))
	if err != nil {
		return "", err
	}

	return "((" + in + ")|(" + missing + "))", nil
}

// buildTermIn renders the TAG set `@f:{a|b}` or the TEXT union `@f:(a|b)`. An empty-string element is its own equality clause
// joined to the set, so the documented empty-value spelling stays the whole term of its clause.
func (t *FilterTranslator) buildTermIn(field string, values []any, ft FieldType) (string, error) {
	terms := make([]string, 0, len(values))
	hasEmpty := false

	for _, v := range values {
		term, err := t.termValue(field, v)
		if err != nil {
			return "", err
		}

		if term == emptyTerm {
			hasEmpty = true
			continue
		}

		terms = append(terms, term)
	}

	open, closing := "(", ")"
	if ft == FieldTypeTag {
		open, closing = "{", "}"
	}

	empty := queryField(field) + ":" + open + emptyTerm + closing
	if ft == FieldTypeText {
		empty = queryField(field) + ":" + emptyTerm
	}

	set := queryField(field) + ":" + open + strings.Join(terms, "|") + closing

	switch {
	case !hasEmpty:
		return set, nil
	case len(terms) == 0:
		return empty, nil
	default:
		return "((" + set + ")|(" + empty + "))", nil
	}
}

// buildIn renders a membership test in the syntax the field's schema
// type calls for.
//
// The TAG form is not universal: `@role:{2|3}` against a NUMERIC field
// matches nothing at all — silently, which is worse than failing — and
// against a TEXT field it is a syntax error. Each type gets the shape it
// actually understands: a union of degenerate ranges for NUMERIC, a tag
// set for TAG, a term union for TEXT.
func (t *FilterTranslator) buildIn(field string, values []any, ft FieldType) (string, error) {
	if len(values) == 0 {
		// A clause and its negation match nothing, including absent fields.
		clause, err := t.buildComparison(field, filter.OpEqual, int64(0), ft)
		if err != nil {
			return "", err
		}

		return "(" + clause + " -" + clause + ")", nil
	}

	rendered := make([]string, 0, len(values))

	switch ft {
	case FieldTypeNumeric:
		for _, v := range values {
			n, err := formatNumericValue(v)
			if err != nil {
				return "", err
			}

			rendered = append(rendered, fmt.Sprintf("%s:[%s %s]", queryField(field), n, n))
		}

		return "(" + strings.Join(rendered, "|") + ")", nil
	case FieldTypeTag, FieldTypeText:
		return t.buildTermIn(field, values, ft)
	default:
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "unknown field type for %q", field)
	}
}

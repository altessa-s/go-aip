// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"strings"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

// Evaluate applies the default evaluator's NULL semantics, the same the rendered SQL has: each predicate is true, false or
// unknown, where unknown stands for a NULL operand the default evaluator also finds unknown, `&&` and `||` absorb unknown as
// SQL's AND and OR do, and only true matches. A nil AST matches every row. maxWork bounds data traversal.
func (p *Program) Evaluate(data map[string]any, maxWork int, decoder evaluator.ValueDecoder) (bool, error) {
	if p.root == nil {
		return true, nil
	}

	b := &budget{left: maxWork, options: p.options}
	b.jsonbStringCompare = p.jsonbStringCompare

	v, err := p.eval(p.root, data, b, decoder)
	if err != nil {
		return false, err
	}

	if v == nil {
		return false, nil
	}

	result, ok := v.(bool)
	if !ok {
		return false, typeError()
	}

	return result, nil
}

// nullable reports whether e is a column that can hold SQL NULL: every column outside contract mode; under the listing
// contract, whose other columns are NOT NULL, only presence columns and JSON values. A NULL column is a null value, which
// equals null and differs from every other value, as in the default evaluator.
func nullable(e *expression, contract bool) bool {
	if e.Tag != filter.NodeKindIdent {
		return false
	}

	return !contract || e.Field.presence || e.Field.json || e.Kind == sqlbase.JSON
}

// nullSafe reports whether a NULL of e is a null value rather than an unknown result: a nullable column or the null literal.
func nullSafe(e *expression, contract bool) bool { return e.IsNull() || nullable(e, contract) }

// computed reports whether e is neither a literal, a list constructor nor a column, so that a NULL it yields (size() of a
// NULL text, an unknown predicate used as a value) stands for an unknown of the default evaluator.
func computed(e *expression) bool {
	return e.Tag != filter.NodeKindLiteral && e.Tag != filter.NodeKindList && e.Tag != filter.NodeKindIdent
}

// eval operates only on typed expressions produced by Compile.
//
//nolint:nilnil,gocognit,gocyclo // nil is an unknown result, not a missing one; one dispatch over every expression tag
func (p *Program) eval(e *expression, data map[string]any, b *budget, d evaluator.ValueDecoder) (any, error) {
	if err := b.Spend(1); err != nil {
		return nil, err
	}

	switch e.Tag {
	case filter.NodeKindLiteral:
		return e.Value, nil
	case filter.NodeKindIdent:
		return p.evalField(e, data, b, d)
	case filter.NodeKindList:
		return p.evalList(e, data, b, d)
	default:
	}

	a, err := p.eval(e.Children[0], data, b, d)
	if err != nil {
		return nil, err
	}

	if e.Tag == filter.NodeKindUnaryOp {
		if a == nil {
			return nil, nil
		}

		value, ok := a.(bool)
		if !ok {
			return nil, typeError()
		}

		return !value, nil
	}

	if e.Tag == filter.NodeKindCall && len(e.Children) == 1 {
		return p.unaryCall(e, a, b)
	}

	c, err := p.eval(e.Children[1], data, b, d)
	if err != nil {
		return nil, err
	}

	if e.Op == filter.OpAnd || e.Op == filter.OpOr {
		return logical(e.Op, a, c), nil
	}

	if e.Tag == filter.NodeKindCall {
		if e.Children[0].Kind == sqlbase.JSON {
			return jsonPredicate(a, c, b)
		}

		if e.Children[0].Array {
			return arrayStringCall(e.Op, a, c, b, e.Matcher)
		}

		return stringCall(e.Op, a, c, b, e.Matcher)
	}

	if e.Op == filter.OpIn {
		return p.evalIn(e, a, c, b)
	}

	return p.evalComparison(e, a, c, b)
}

// evalField reads a column. Under the listing contract a NULL in a NOT NULL column reads as the column's zero value (an
// array as empty), as the contract stores an absent value; a UUID column has no zero value and stays NULL.
func (p *Program) evalField(e *expression, data map[string]any, b *budget, d evaluator.ValueDecoder) (any, error) {
	v, _, err := readField(e.Field, data, b, d)
	if err != nil || v != nil || !p.contract || nullable(e, true) || e.Kind == sqlbase.UUID {
		return v, err
	}

	if e.Array {
		return arrayValue{dimensions: []sqltype.Dimension{{LowerBound: 1}}, values: []any{}}, nil
	}

	return normalize(zeroValue(e.Kind), e.Kind, nil, b)
}

func (p *Program) evalList(e *expression, data map[string]any, b *budget, d evaluator.ValueDecoder) (any, error) {
	out := arrayValue{dimensions: e.Dimensions, values: make([]any, 0, len(e.Children))}
	for _, c := range e.Children {
		v, err := p.eval(c, data, b, d)
		if err != nil {
			return nil, err
		}

		if nested, ok := v.(arrayValue); ok {
			out.values = append(out.values, nested.values...)
		} else {
			// ARRAY coerces every element to its common type before comparison.
			if v != nil && c.Kind != e.Kind {
				v, err = normalize(v, e.Kind, nil, b)
				if err != nil {
					return nil, err
				}
			}

			out.values = append(out.values, v)
		}
	}

	return out, nil
}

// listLeaves returns the scalar elements of a list constructor in the order evalList flattens their values.
func listLeaves(e *expression, out []*expression) []*expression {
	for _, c := range e.Children {
		if c.Tag == filter.NodeKindList {
			out = listLeaves(c, out)
		} else {
			out = append(out, c)
		}
	}

	return out
}

// unaryCall evaluates has(), exists(), size() and substring().
//
//nolint:nilnil // nil is the unknown size() of a NULL text, bytes or JSON value.
func (p *Program) unaryCall(e *expression, a any, b *budget) (any, error) {
	target := e.Children[0]
	if e.Op == filter.OpSubstring {
		return substringValue(e.Span, a, b)
	}

	if p.contract && e.Op == filter.OpSize && a == nil && target.Field.presence {
		return int64(0), nil
	}

	if e.Op == filter.OpHas || e.Op == filter.OpExists {
		if !p.contract || nullable(target, true) {
			return a != nil, nil
		}

		if target.Array {
			values, ok := a.(arrayValue)
			return ok && len(values.values) != 0, nil
		}

		if target.Kind == sqlbase.UUID {
			return true, nil
		}

		if a == nil {
			return nil, nil
		}

		order, err := compareValue(a, zeroValue(target.Kind), target.Kind, b)

		return order != 0, err
	}

	if a == nil {
		if target.Array && target.Tag == filter.NodeKindIdent {
			return int64(0), nil // the default evaluator's size() of an absent array
		}

		return nil, nil
	}

	switch x := a.(type) {
	case string:
		return int64(utf8.RuneCountInString(x)), nil
	case []byte:
		return int64(len(x)), nil
	case arrayValue:
		return int64(len(x.values)), nil
	case []any:
		return int64(len(x)), nil
	default:
		return nil, typeError()
	}
}

// substringValue returns the code points [start, start+length) of the text a, to its end for a negative length. It is unknown
// (nil) for a NULL text and for a text shorter than the guard, as in the default evaluator.
//
//nolint:nilnil // nil is the unknown substring() of a NULL or too short text.
func substringValue(span sqltype.Span, a any, b *budget) (any, error) {
	if a == nil {
		return nil, nil
	}

	s, ok := a.(string)
	if !ok {
		return nil, typeError()
	}

	if err := b.Spend(len(s)); err != nil {
		return nil, err
	}

	if int64(utf8.RuneCountInString(s)) < span.Guard {
		return nil, nil
	}

	begin := runeOffset(s, span.Start)
	if span.Length < 0 {
		return s[begin:], nil
	}

	return s[begin : begin+runeOffset(s[begin:], span.Length)], nil
}

// runeOffset returns the byte offset of the n-th code point of s; s holds at least n.
func runeOffset(s string, n int64) int {
	offset := 0
	for ; n > 0; n-- {
		_, size := utf8.DecodeRuneInString(s[offset:])
		offset += size
	}

	return offset
}

func logical(op filter.Operator, a, b any) any {
	if op == filter.OpAnd {
		if a == false || b == false {
			return false
		}

		if a == nil || b == nil {
			return nil
		}

		return true
	}

	if a == true || b == true {
		return true
	}

	if a == nil || b == nil {
		return nil
	}

	return false
}

// stringCall applies a string function. A NULL target contains nothing, so the function is false for it, as in the default
// evaluator; a NULL argument of a non-NULL target is unknown.
//
//nolint:nilnil // A NULL argument is unknown.
func stringCall(op filter.Operator, a, c any, b *budget, matcher filter.RegexMatcher) (any, error) {
	if a == nil {
		return false, nil
	}

	if c == nil {
		return nil, nil
	}

	x, ok := a.(string)
	if !ok {
		return nil, typeError()
	}

	y, ok := c.(string)
	if !ok {
		return nil, typeError()
	}

	if err := b.Spend(len(x) + len(y)); err != nil {
		return nil, err
	}

	switch op {
	case filter.OpContains:
		return strings.Contains(x, y), nil
	case filter.OpStartsWith:
		return strings.HasPrefix(x, y), nil
	case filter.OpEndsWith:
		return strings.HasSuffix(x, y), nil
	case filter.OpMatches:
		return matcher.MatchString(x), nil
	default:
		return nil, unsupported()
	}
}

// evalComparison evaluates a comparison other than `in`.
//
//nolint:nilnil,gocognit,gocyclo // nil is an unknown result.
func (p *Program) evalComparison(e *expression, a, c any, b *budget) (any, error) {
	left, right := e.Children[0], e.Children[1]

	if left.IsNull() || right.IsNull() {
		field, value := left, a
		if left.IsNull() {
			field, value = right, c
		}

		if p.contract && field.Tag == filter.NodeKindIdent && !nullable(field, true) {
			return contractNullComparison(e.Op, field, value, b)
		}

		if value == nil && computed(field) {
			return nil, nil
		}

		return (value == nil) == (e.Op == filter.OpEqual), nil
	}

	if left.Array && !right.Array {
		if c == nil && computed(right) {
			return nil, nil
		}

		return arrayPredicate(e.Op, a, c, sqltype.ComparisonKind(left.Kind, right.Kind), b)
	}

	if a == nil || c == nil {
		if !e.Op.IsEquality() || (a == nil && !nullSafe(left, p.contract)) || (c == nil && !nullSafe(right, p.contract)) {
			return nil, nil
		}

		return (a == nil && c == nil) == (e.Op == filter.OpEqual), nil
	}

	k := sqltype.ComparisonKind(left.Kind, right.Kind)
	if left.Array && right.Array && left.Kind != right.Kind {
		var err error
		if left.Kind != k {
			a, err = coerceArray(a, k, b)
			if err != nil {
				return nil, err
			}
		}

		if right.Kind != k {
			c, err = coerceArray(c, k, b)
			if err != nil {
				return nil, err
			}
		}
	}

	order, err := compareValue(a, c, k, b)
	if err != nil {
		return nil, err
	}

	return comparison(e.Op, order), nil
}

// contractNullComparison evaluates `field == null` or `field != null` on a NOT NULL column of the listing contract, where null
// means the zero value (an empty array).
//
//nolint:nilnil // A NULL in a NOT NULL column is unknown, as it is in SQL.
func contractNullComparison(op filter.Operator, field *expression, value any, b *budget) (any, error) {
	if field.Kind == sqlbase.UUID {
		return op == filter.OpNotEqual, nil
	}

	if value == nil {
		return nil, nil
	}

	var equal bool

	if field.Array {
		values, ok := value.(arrayValue)
		if !ok {
			return nil, typeError()
		}

		equal = len(values.values) == 0
	} else {
		order, err := compareValue(value, zeroValue(field.Kind), field.Kind, b)
		if err != nil {
			return nil, err
		}

		equal = order == 0
	}

	return equal == (op == filter.OpEqual), nil
}

// evalIn evaluates `a in c`. A list constructor holds a null value when a null literal or a NULL column is among its elements,
// and a null left operand is in it exactly then, as in the default evaluator. A null left operand is in no empty or NULL
// array value and unknown for any other: SQL's = ANY cannot find it equal to a NULL element, as the default evaluator does.
//
//nolint:nilnil,gocognit,gocyclo // nil is an unknown result.
func (p *Program) evalIn(e *expression, a, c any, b *budget) (any, error) {
	left, right := e.Children[0], e.Children[1]
	if a == nil && !nullSafe(left, p.contract) && (!p.contract || !left.Array) {
		return nil, nil
	}

	k := sqltype.ComparisonKind(left.Kind, right.Kind)

	values, ok := c.(arrayValue)
	if c != nil && !ok {
		return nil, typeError()
	}

	if p.contract && left.Array {
		fieldValues, _ := a.(arrayValue)
		for _, fieldValue := range fieldValues.values {
			if fieldValue == nil {
				continue
			}

			for _, candidate := range values.values {
				if candidate == nil {
					continue
				}

				order, err := compareValue(fieldValue, candidate, k, b)
				if err != nil {
					return nil, err
				}

				if order == 0 {
					return true, nil
				}
			}
		}

		return false, nil
	}

	if right.Tag != filter.NodeKindList {
		return anyComparison(a, values, k, b)
	}

	unknown := false

	for i, leaf := range listLeaves(right, nil) {
		v := values.values[i]
		if v == nil {
			if !nullSafe(leaf, p.contract) {
				unknown = true
			} else if a == nil {
				return true, nil
			}

			continue
		}

		if a == nil {
			continue
		}

		order, err := compareValue(a, v, k, b)
		if err != nil {
			return nil, err
		}

		if order == 0 {
			return true, nil
		}
	}

	if unknown {
		return nil, nil
	}

	return false, nil
}

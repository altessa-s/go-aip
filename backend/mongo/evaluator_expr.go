// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"slices"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// This file evaluates the predicates the MongoDB translator renders as aggregation expressions under $expr: a
// field compared with another field (or with a size()), a field argument of contains, startsWith and endsWith, a list of in holding
// a field, and substring(). Their semantics are those of the translator's expression forms:
//
//   - A field operand reads the value the aggregation path resolves to (through a list of documents, the list of the values found);
//     missing and null are the same null, and a field declared by [WithSchemaZeroWhenAbsent] reads its zero value instead.
//     Values compare as whole values; only a field declared by [filter.WithArrayFields] compared with a non-array operand also
//     matches when it holds that operand, as `tags == "x"` does.
//   - == holds when both operands are equal, two nulls included; != is its negation.
//   - <, <=, >, >= hold only when both operands are non-null and of one type class: numbers across the integer, float and decimal
//     types, otherwise the same BSON type.
//   - A size() or substring() operand that has no value (a value without a size; a non-string target or a range beyond its end),
//     and a null [WithSchemaPresenceFields] field, make every comparison false, != included.
//   - A string function with a field argument holds only when the target and the argument are both strings.

// isStringExprCall reports whether n is contains, startsWith or endsWith with a single argument that is not a literal, which the
// MongoDB translator renders as an aggregation expression.
func isStringExprCall(n *filter.CallNode) bool {
	switch n.Op {
	case filter.OpContains, filter.OpStartsWith, filter.OpEndsWith:
	default:
		return false
	}

	if len(n.Args) != 1 {
		return false
	}

	_, isLiteral := n.Args[0].(*filter.LiteralNode)

	return !isLiteral
}

// exprValue is the value of one operand of an expression predicate.
type exprValue struct {
	value    any  // the value, in its stored form; nil for null and for an operand without a value
	array    bool // a field declared by filter.WithArrayFields
	nullable bool // without a value (nil), every comparison is false
}

// operandValue evaluates the operand n of an expression predicate: a literal, a field, a size() or a substring() call. The filter is
// validated, so any other node is a malformed one.
func (e *evaluation) operandValue(n filter.Node) (exprValue, error) {
	switch x := n.(type) {
	case *filter.LiteralNode:
		if err := e.Op(); err != nil {
			return exprValue{}, err
		}

		return exprValue{value: x.Value}, nil
	case *filter.IdentNode:
		v, err := e.exprField(x)

		return exprValue{value: v, array: e.policy.IsArrayField(x.Name), nullable: e.schema.IsPresenceField(x.Name)}, err
	case *filter.CallNode:
		switch x.Op {
		case filter.OpSize:
			return e.exprSize(x)
		case filter.OpSubstring:
			return e.exprSubstring(x)
		default:
		}
	}

	return exprValue{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a comparison operand", n)
}

// exprPath resolves the field ident of an expression operand: it checks the allow-list and returns the mapped path.
func (e *evaluation) exprPath(n filter.Node) (*filter.IdentNode, string, error) {
	if err := e.Op(); err != nil {
		return nil, "", err
	}

	ident, ok := n.(*filter.IdentNode)
	if !ok || ident == nil {
		return nil, "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a field operand", n)
	}

	if !e.policy.IsFieldAllowed(ident.Name) {
		return nil, "", coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", ident.Name)
	}

	return ident, e.policy.ApplyFieldMapping(ident.Name), nil
}

// exprResolved returns the value the aggregation path resolves to at path: nil when the path is missing, null, or left out of the
// stored document ([evaluation.omittedAt]).
func (e *evaluation) exprResolved(path string) (any, error) {
	if e.omittedAt(path) {
		return nil, e.Err()
	}

	v, _, _ := e.ResolvePath(e.Data(), path)

	return v, e.Err()
}

// exprField is the value of a field operand ($ifNull of the field path): the value the aggregation path resolves to, null when
// missing, and the declared zero of a field declared by [WithSchemaZeroWhenAbsent] instead of null.
func (e *evaluation) exprField(n filter.Node) (any, error) {
	ident, path, err := e.exprPath(n)
	if err != nil {
		return nil, err
	}

	v, err := e.exprResolved(path)
	if err != nil || v != nil {
		return v, err
	}

	if kind, declared := e.schema.ZeroWhenAbsent(ident.Name); declared {
		return zeroOf(kind), nil
	}

	return nil, nil //nolint:nilnil // null is a valid field value
}

// exprSize is a size() operand: a missing or null value counts as an empty array, an array by its elements, a string by its code
// points; any other value has no size.
func (e *evaluation) exprSize(call *filter.CallNode) (exprValue, error) {
	if err := e.Op(); err != nil {
		return exprValue{}, err
	}

	_, path, err := e.exprPath(call.Target)
	if err != nil {
		return exprValue{}, err
	}

	v, err := e.exprResolved(path)
	if err != nil {
		return exprValue{}, err
	}

	if v == nil {
		return exprValue{value: int64(0), nullable: true}, nil
	}

	length, measured := e.sizeOf(v)
	if err := e.Err(); err != nil {
		return exprValue{}, err
	}

	if !measured {
		return exprValue{nullable: true}, nil
	}

	return exprValue{value: length, nullable: true}, nil
}

// exprSubstring is a substring(start[, end]) operand: the code points [start, end) of a string field, or none when the field does
// not hold a string or end (start, without end) is beyond its length. The bounds are validated integer literals.
func (e *evaluation) exprSubstring(call *filter.CallNode) (exprValue, error) {
	if err := e.Op(); err != nil {
		return exprValue{}, err
	}

	target, err := e.exprField(call.Target)
	if err != nil {
		return exprValue{}, err
	}

	start, end, hasEnd, err := substringBounds(call.Args)
	if err != nil {
		return exprValue{}, err
	}

	s, ok := target.(string)
	if !ok {
		return exprValue{nullable: true}, nil
	}

	if !e.ChargeScan(len(s)) {
		return exprValue{}, e.Err()
	}

	length := int64(utf8.RuneCountInString(s))

	limit := end
	if !hasEnd {
		end, limit = length, start
	}

	if length < limit {
		return exprValue{nullable: true}, nil
	}

	return exprValue{value: runeSlice(s, start, end), nullable: true}, nil
}

// runeSlice returns the code points [start, end) of s, which has at least end of them.
func runeSlice(s string, start, end int64) string {
	from, to, i := len(s), len(s), int64(0)
	for offset := range s {
		if i == start {
			from = offset
		}

		if i == end {
			to = offset
			break
		}

		i++
	}

	return s[from:to]
}

// exprComparison evaluates `left op right` as the MongoDB translator's aggregation expression does.
func (e *evaluation) exprComparison(op filter.Operator, left, right filter.Node) (bool, error) {
	l, err := e.operandValue(left)
	if err != nil {
		return false, err
	}

	r, err := e.operandValue(right)
	if err != nil {
		return false, err
	}

	return e.exprCompare(op, l, r), e.Err()
}

// exprCompare applies op to two expression operands.
func (e *evaluation) exprCompare(op filter.Operator, l, r exprValue) bool {
	if (l.nullable && l.value == nil) || (r.nullable && r.value == nil) {
		return false
	}

	switch op {
	case filter.OpEqual:
		return e.exprEquality(l, r)
	case filter.OpNotEqual:
		return !e.exprEquality(l, r) && !e.Exhausted()
	default:
		if l.value == nil || r.value == nil || !sameTypeClass(e.Encoding(), l.value, r.value) {
			return false
		}

		c, ok := e.exprOrder(l.value, r.value)
		if !ok {
			return false
		}

		holds, _ := evaluator.CompareResult(op, c) //nolint:errcheck // op is a validated ordering operator

		return holds
	}
}

// exprEquality reports whether l equals r; an array field compared with an operand that is not one also matches when it holds it.
func (e *evaluation) exprEquality(l, r exprValue) bool {
	if e.exprEqual(l.value, r.value) {
		return true
	}

	switch {
	case l.array && !r.array:
		return e.exprHolds(l.value, r.value)
	case r.array && !l.array:
		return e.exprHolds(r.value, l.value)
	default:
		return false
	}
}

// exprHolds reports whether list is an array holding an element equal to v (aggregation $in).
func (e *evaluation) exprHolds(list, v any) bool {
	elements, ok := list.([]any)
	if !ok || !e.Charge(len(elements)) {
		return false
	}

	return slices.ContainsFunc(elements, func(element any) bool { return e.exprEqual(e.Stored(element), v) })
}

// exprEqual reports whether a equals b as an aggregation expression compares whole values.
func (e *evaluation) exprEqual(a, b any) bool {
	c, ok := e.exprOrder(a, b)
	return ok && c == 0
}

// exprIn evaluates `left in list` whose list holds an element that is not a literal, or whose left side is a substring(), as the
// MongoDB translator does: the disjunction of the membership of left in the literal elements (a query $in; with a substring() on
// the left, an expression equality each) and of one expression equality per other element. left is evaluated once.
func (e *evaluation) exprIn(left filter.Node, list *filter.ListNode) (bool, error) {
	if err := e.Enter(); err != nil {
		return false, err
	}
	defer e.Leave()

	substring := isSubstring(left)

	var literals []filter.Node

	l, err := e.operandValue(left)
	if err != nil {
		return false, err
	}

	found := false

	for _, element := range list.Elements {
		if _, isLiteral := element.(*filter.LiteralNode); isLiteral && !substring {
			literals = append(literals, element)
			continue
		}

		r, err := e.operandValue(element)
		if err != nil {
			return false, err
		}

		found = found || e.exprCompare(filter.OpEqual, l, r)
	}

	if err := e.Err(); err != nil {
		return false, err
	}

	if found || len(literals) == 0 {
		return found, nil
	}

	return e.mongoIn(left, &filter.ListNode{Elements: literals})
}

// exprStringCall evaluates contains, startsWith or endsWith with a field argument: it holds when the target and the argument are
// both strings and the target satisfies the function of the argument.
func (e *evaluation) exprStringCall(n *filter.CallNode) (bool, error) {
	a, err := e.exprField(n.Target)
	if err != nil {
		return false, err
	}

	b, err := e.exprField(n.Args[0])
	if err != nil {
		return false, err
	}

	s, sOK := a.(string)
	arg, argOK := b.(string)

	if !sOK || !argOK {
		return false, nil
	}

	if !e.ChargeScan(len(s)) {
		return false, e.Err()
	}

	test, _ := stringTest(n.Op)(arg) //nolint:errcheck // a string test never fails

	return test(s), nil
}

// queryHasString reports whether a query {path: {$type: "string"}} on the field ident matches: a value the query reaches at its
// path (an element of a list at its end included) is a string.
func (e *evaluation) queryHasString(ident *filter.IdentNode) bool {
	path := e.policy.ApplyFieldMapping(ident.Name)
	if e.omittedAt(path) {
		return false
	}

	return slices.ContainsFunc(e.QueryValues(e.Data(), path, true, nil), func(v any) bool {
		_, ok := v.(string)
		return ok
	})
}

// presenceStringGuard is the type guard of a negated string function reading a [WithSchemaPresenceFields] field, as its
// target or as a field argument: the target and every presence field argument must hold a string, or the negation is false.
// applies is false when the call reads no presence field.
func (e *evaluation) presenceStringGuard(call *filter.CallNode) (holds, applies bool) {
	semantics := e.schema

	target, targetIdent := call.Target.(*filter.IdentNode)
	applies = targetIdent && semantics.IsPresenceField(target.Name)

	for _, arg := range call.Args {
		if ident, ok := arg.(*filter.IdentNode); ok && semantics.IsPresenceField(ident.Name) {
			applies = true
		}
	}

	if !applies {
		return false, false
	}

	if targetIdent && !e.queryHasString(target) {
		return false, true
	}

	for _, arg := range call.Args {
		if ident, ok := arg.(*filter.IdentNode); ok && semantics.IsPresenceField(ident.Name) && !e.queryHasString(ident) {
			return false, true
		}
	}

	return true, true
}

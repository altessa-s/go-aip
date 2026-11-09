// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"reflect"
	"slices"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// This file implements the evaluator profile of [Schema] ([Schema.BindEvaluator]): every predicate matches a document exactly when
// the query the MongoDB translator builds for it does, and the per-document outcome of a predicate is a bool, never an error. The filter is
// validated before any data is read (evaluator_validate.go), with the rules of the translator, so the evaluation below handles only
// the shapes the translator accepts.

// trueLiteral is the operand of a bare field used as a predicate: the MongoDB translator emits `active` as {active: true}.
var trueLiteral = &filter.LiteralNode{Value: true}

// fieldValue is a field operand resolved under MongoDB semantics.
type fieldValue struct {
	path    string // the mapped path
	value   any    // the value the aggregation path resolves to; nil when absent or null
	present bool   // the path exists (a null value included), in at least one document of a list
	crossed bool   // the path went through a list of documents, so value is the list of the values it resolved to
	zero    bool   // a zero-when-absent field whose query path matches null: value is the kind's zero value
	omitted bool   // a field declared by WithOmitEmpty whose data value is empty: read as absent
}

// acceptPredicate visits n where a bool is expected: a bare field is the test field == true.
func (e *evaluation) acceptPredicate(n filter.Node) (any, error) {
	if ident, ok := n.(*filter.IdentNode); ok {
		return e.mongoComparison(filter.OpEqual, ident, trueLiteral)
	}

	return n.Accept(e)
}

// queryField resolves the aggregation value and applies the translator's document-wide
// null guard for zero-when-absent fields. A query path matches null when any candidate
// is null or missing; an empty traversal supplies no candidate and does not match null.
// If null matches, every predicate has the result it would have on the kind's zero:
// absentAs either ORs the null match with the base query or ANDs its complement.
func (e *evaluation) queryField(ident *filter.IdentNode) (fieldValue, error) {
	if err := e.Op(); err != nil {
		return fieldValue{}, err
	}

	if !e.policy.IsFieldAllowed(ident.Name) {
		return fieldValue{}, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", ident.Name)
	}

	path := e.policy.ApplyFieldMapping(ident.Name)
	omitted := e.omittedAt(path)

	var (
		v                any
		present, crossed bool
	)
	if !omitted {
		v, present, crossed = e.ResolvePath(e.Data(), path)
	}

	if err := e.Err(); err != nil {
		return fieldValue{}, err
	}

	if kind, ok := e.schema.ZeroWhenAbsent(ident.Name); ok {
		nullMatches := omitted
		if !omitted {
			values := e.QueryValues(e.Data(), path, true, nil)
			if err := e.Err(); err != nil {
				return fieldValue{}, err
			}

			nullMatches = slices.ContainsFunc(values, func(value any) bool { return value == nil })
		}

		if nullMatches {
			return fieldValue{path: path, value: zeroOf(kind), present: true, zero: true}, nil
		}
	}

	return fieldValue{path: path, value: v, present: present, crossed: crossed, omitted: omitted}, nil
}

// candidates returns the values a query predicate on f tests, as a MongoDB query traverses the path: a list at the end of the path
// yields its elements; a list of documents on the way yields the values found in each document, and null for a document that lacks
// the rest of the path; an absent path yields null. An empty list, or one without documents on the way, yields nothing. It fails
// with [filter.ErrDataLimitExceeded] when the data budget runs out on the way.
func (e *evaluation) candidates(f fieldValue) ([]any, error) {
	if f.zero {
		return []any{f.value}, nil
	}

	if f.omitted {
		return []any{nil}, nil // an absent path yields null
	}

	values := e.QueryValues(e.Data(), f.path, true, nil)
	if err := e.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

// mongoComparison evaluates `left op right` under MongoDB semantics: left is a field or a size() call, right a literal; a comparison
// the translator renders as an aggregation expression (two field references, a substring()) is [evaluation.exprComparison].
func (e *evaluation) mongoComparison(op filter.Operator, left, right filter.Node) (bool, error) {
	if needsExpr(left, right) {
		return e.exprComparison(op, left, right)
	}

	if isSizeCall(left) {
		return e.mongoSizeComparison(op, left.(*filter.CallNode), right) //nolint:errcheck,forcetypeassert // checked by isSizeCall
	}

	ident, ok := left.(*filter.IdentNode)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field reference, got %T", left)
	}

	f, err := e.queryField(ident)
	if err != nil {
		return false, err
	}

	lit, err := e.mongoOperand(ident.Name, right)
	if err != nil {
		return false, err
	}

	if e.schema.IsPresenceField(ident.Name) {
		if f.value == nil {
			return lit == nil && op == filter.OpEqual, nil
		}

		if lit == nil {
			return op == filter.OpNotEqual, nil
		}
	}

	if lit == nil {
		if kind, declared := e.schema.ZeroWhenAbsent(ident.Name); declared {
			lit = zeroOf(kind)
		}
	}

	if lit == nil && e.policy.PortableContract() && e.policy.IsArrayField(ident.Name) && !f.crossed {
		if f.value == nil || isArrayValue(f.value) {
			hasElements := mongoArrayHasElements(f.value)
			if op == filter.OpNotEqual {
				return hasElements, nil
			}

			return !hasElements, nil
		}
	}
	// != holds when no candidate is equal; every other comparison when one candidate satisfies it.
	match := op
	if op == filter.OpNotEqual {
		match = filter.OpEqual
	}

	candidates, err := e.candidates(f)
	if err != nil {
		return false, err
	}

	for _, v := range candidates {
		if !e.ChargeComparison(v, lit) {
			return false, e.Err()
		}

		if mongoCompare(e.Encoding(), match, v, lit) {
			return op != filter.OpNotEqual, nil
		}
	}

	return op == filter.OpNotEqual, nil
}

// mongoOperand evaluates the operand a field is compared with; for a [filter.FieldKindDecimal] field it is read as an exact decimal, as the
// MongoDB translator emits it.
func (e *evaluation) mongoOperand(field string, operand filter.Node) (any, error) {
	if operand == trueLiteral {
		return true, nil // the implicit operand of a bare field is not a node of the filter
	}

	v, err := operand.Accept(e)
	if err != nil || v == nil || e.policy.FieldKind(field) != filter.FieldKindDecimal {
		return v, err
	}

	return e.policy.DecimalOperand(operand, v)
}

// mongoIn evaluates `left in right` under MongoDB semantics: a field matches when one of its candidates equals an element of the
// list; `x in field` on a field declared by [filter.WithArrayFields] is the equality field == x, for a literal, a substring() or a
// scalar field x; a list holding a field, or a substring() on the left, is [evaluation.exprIn].
func (e *evaluation) mongoIn(left, right filter.Node) (bool, error) {
	if field, isField := right.(*filter.IdentNode); isField && e.policy.IsArrayField(field.Name) && (!filter.IsFieldReference(left) || e.isScalarField(left)) {
		return e.mongoComparison(filter.OpEqual, field, left)
	}

	if list, isList := right.(*filter.ListNode); isList && list != nil && (hasDynamicElement(list) || isSubstring(left)) {
		return e.exprIn(left, list)
	}

	ident, ok := left.(*filter.IdentNode)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field reference, got %T", left)
	}

	f, err := e.queryField(ident)
	if err != nil {
		return false, err
	}

	items, err := e.mongoList(ident.Name, right)
	if err != nil {
		return false, err
	}

	candidates, err := e.candidates(f)
	if err != nil {
		return false, err
	}

	member := func(v any) bool {
		for _, item := range items {
			if !e.ChargeComparison(v, item) {
				return true // stop: the evaluation fails
			}

			if mongoCompare(e.Encoding(), filter.OpEqual, v, item) {
				return true
			}
		}

		return false
	}
	found := slices.ContainsFunc(candidates, member)

	return found, e.Err()
}

// mongoList evaluates the list operand of in, element by element ([evaluation.mongoOperand]) under the same budget as
// [evaluation.VisitList].
func (e *evaluation) mongoList(field string, right filter.Node) ([]any, error) {
	list, ok := right.(*filter.ListNode)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "in requires a list on the right side")
	}

	if err := e.Enter(); err != nil {
		return nil, err
	}

	defer e.Leave()

	items := make([]any, len(list.Elements))
	for i, el := range list.Elements {
		v, err := e.mongoOperand(field, el)
		if err != nil {
			return nil, err
		}

		items[i] = v
	}

	return items, nil
}

// mongoStringCall evaluates contains, startsWith, endsWith and matches under MongoDB semantics: a string candidate satisfies test;
// any other value does not.
func (e *evaluation) mongoStringCall(n *filter.CallNode, label string, test func(arg string) (func(string) bool, error)) (bool, error) {
	ident, ok := n.Target.(*filter.IdentNode)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires a field", label)
	}

	f, err := e.queryField(ident)
	if err != nil {
		return false, err
	}

	if len(n.Args) != 1 {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires exactly 1 argument", label)
	}

	av, err := n.Args[0].Accept(e)
	if err != nil {
		return false, err
	}

	arg, ok := av.(string)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s argument must be a string", label)
	}

	fn, err := test(arg)
	if err != nil {
		return false, err
	}

	candidates, err := e.candidates(f)
	if err != nil {
		return false, err
	}

	holds := func(v any) bool {
		s, isString := mongoString(e.Encoding(), v) // $regex matches a symbol too
		if !isString {
			return false
		}

		if !e.chargeStringTest(n.Op, s, arg) {
			return true // stop: the evaluation fails
		}

		return fn(s)
	}
	found := slices.ContainsFunc(candidates, holds)

	return found, e.Err()
}

// mongoHas evaluates has() under MongoDB semantics: a field declared by [WithSchemaZeroWhenAbsent] is non-zero, any other
// field exists when its path does (with a null value too, as $exists reads it).
func (e *evaluation) mongoHas(n *filter.CallNode) (bool, error) {
	ident, ok := n.Target.(*filter.IdentNode)
	if !ok {
		return false, coreerrs.Wrap(filter.ErrInvalidExpression, "has() requires an identifier")
	}

	f, err := e.queryField(ident)
	if err != nil {
		return false, err
	}

	if kind, declared := e.schema.ZeroWhenAbsent(ident.Name); declared {
		return e.mongoComparison(filter.OpNotEqual, ident, &filter.LiteralNode{Value: zeroOf(kind)})
	}

	if e.schema.IsPresenceField(ident.Name) {
		return f.present && f.value != nil, nil
	}

	if e.policy.PortableContract() && e.policy.IsArrayField(ident.Name) && !f.crossed {
		return f.present && mongoArrayHasElements(f.value), nil
	}

	return f.present, nil
}

// isScalarField reports whether n is a field not declared by [filter.WithArrayFields].
func (e *evaluation) isScalarField(n filter.Node) bool {
	ident, ok := n.(*filter.IdentNode)
	return ok && ident != nil && !e.policy.IsArrayField(ident.Name)
}

func mongoArrayHasElements(value any) bool {
	values, ok := value.([]any)
	return ok && len(values) != 0
}

// mongoSizeComparison evaluates `size(field) op operand` the way the MongoDB translator's query does. size() measures an array by
// its elements and a string by its code points; any other value has no size, so only != holds. The ordering comparisons, and every
// comparison on a field declared by [filter.WithArrayFields] or measured so under [WithSchemaSizeZeroWhenAbsent],
// measure the value the aggregation path resolves to, an absent or null value counting as
// an empty array. == and != on any other field test the value of the path as a query does ($size): through a list of documents they
// test each array found at the end of the path (the strings there have no size); an absent or null value has no size. A fractional
// operand equals no size. The operand is a valid [CheckSizeOperand] value.
func (e *evaluation) mongoSizeComparison(op filter.Operator, call *filter.CallNode, operand filter.Node) (bool, error) {
	ident, ok := call.Target.(*filter.IdentNode)
	if !ok {
		return false, coreerrs.Wrap(filter.ErrInvalidExpression, "size() takes a field")
	}

	v, err := operand.Accept(e)
	if err != nil {
		return false, err
	}

	f, err := e.queryField(ident)
	if err != nil {
		return false, err
	}

	resolved := e.schema.MeasuresResolvedSize(ident.Name, e.policy)
	if op.IsEquality() && f.crossed && !resolved {
		found := false

		for _, leaf := range e.QueryValues(e.Data(), f.path, false, nil) {
			if !e.ChargeComparison(leaf, v) {
				break
			}

			if list, isList := leaf.([]any); isList && mongoCompare(e.Encoding(), filter.OpEqual, int64(len(list)), v) {
				found = true
				break
			}
		}

		if err := e.Err(); err != nil {
			return false, err
		}

		return found == (op == filter.OpEqual), nil
	}

	length, measured := e.sizeOf(f.value)
	if err := e.Err(); err != nil {
		return false, err
	}

	if !measured && f.value == nil && (resolved || !op.IsEquality()) {
		length, measured = 0, true
	}

	if !measured {
		return op == filter.OpNotEqual, nil
	}

	return mongoCompare(e.Encoding(), op, length, v), nil
}

// isSizeCall reports whether n is a size() call.
func isSizeCall(n filter.Node) bool {
	call, ok := n.(*filter.CallNode)
	return ok && call.Op == filter.OpSize
}

// isArrayValue reports whether value is a slice or an array that is not bytes.
func isArrayValue(value any) bool {
	rv := reflect.ValueOf(value)

	return rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) &&
		(rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8)
}

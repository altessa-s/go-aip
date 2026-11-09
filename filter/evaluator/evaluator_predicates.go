// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"reflect"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// evalComparison evaluates comparison operators.
func (e *evaluation) evalComparison(op filter.Operator, left, right filter.Node) (any, error) {
	if result, ok, err := e.compareArrayNull(op, left, right); ok || err != nil {
		return result, err
	}

	if ident, ok := left.(*filter.IdentNode); ok && ident != nil {
		if lit, ok := right.(*filter.LiteralNode); ok && lit != nil {
			if f := e.config.Field(ident.Name); !f.IsArray() {
				return e.compareField(op, ident.Name, f, lit)
			}
		}
	}

	if err := e.config.CheckComparison(left, right); err != nil {
		return nil, err
	}

	elems, elementwise, err := e.arrayElements(left)
	if err != nil {
		return nil, err
	}

	if elementwise {
		return e.compareArray(op, left, right, elems)
	}

	return e.compareOperands(op, left, right)
}

// compareArrayNull evaluates `array == null` and `array != null` under the portable contract, where they test whether a
// present array has elements; ok is false when the comparison is not of that form or the array is absent.
func (e *evaluation) compareArrayNull(op filter.Operator, left, right filter.Node) (result any, ok bool, err error) {
	ident, isIdent := left.(*filter.IdentNode)
	if !isIdent || !e.config.PortableContract() || !e.config.IsArrayField(ident.Name) {
		return nil, false, nil
	}

	if lit, isLiteral := right.(*filter.LiteralNode); !isLiteral || lit == nil || lit.Value != nil {
		return nil, false, nil
	}

	value, present := e.WalkField(e.config.ApplyFieldMapping(ident.Name))
	if err := e.Err(); err != nil {
		return nil, false, err
	}

	if !present || !isArrayValue(value) {
		return nil, false, nil
	}

	hasElement := hasArrayFirstElement(value)
	if op == filter.OpNotEqual {
		return hasElement, true, nil
	}

	return !hasElement, true, nil
}

// compareArray compares every element of an array field, elems, with the right operand.
func (e *evaluation) compareArray(op filter.Operator, left, right filter.Node, elems []any) (any, error) {
	rv, err := right.Accept(e)
	if err != nil {
		return nil, err
	}

	decimalLeft := e.isDecimalField(left)
	if decimalLeft {
		for i, el := range elems {
			elems[i] = e.decimalData(el)
		}
	}

	if e.isDecimalField(right) {
		rv = e.decimalData(rv)
	}

	rv = e.comparable(rv)
	if decimalLeft {
		if rv, err = e.decimalOperand(right, rv); err != nil {
			return nil, err
		}
	}

	return e.compareElements(op, elems, right, rv)
}

// compareOperands compares two scalar operands.
func (e *evaluation) compareOperands(op filter.Operator, left, right filter.Node) (any, error) {
	lv, err := left.Accept(e)
	if err != nil {
		return nil, err
	}

	rv, err := right.Accept(e)
	if err != nil {
		return nil, err
	}

	if e.isDecimalField(left) {
		return e.compareDecimalField(op, lv, right, rv)
	}

	if e.isDecimalField(right) {
		rv = e.decimalData(rv)
	}

	rv, lv = e.comparable(rv), e.comparable(lv)

	l, r := exactLiteral(rv, left, lv), exactLiteral(lv, right, rv)
	if !e.budget.chargeComparison(l, r) {
		return nil, e.Err()
	}

	return e.compare(op, l, r)
}

func hasArrayFirstElement(value any) bool {
	rv := reflect.ValueOf(value)

	return rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) &&
		(rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8) && rv.Len() != 0
}

func isArrayValue(value any) bool {
	rv := reflect.ValueOf(value)

	return rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) &&
		(rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8)
}

// compareField is [evaluation.evalComparison] of `name op lit` on a field that is not declared by [filter.WithArrayFields], the shape a
// filter compares most, with what the options declare about the field (f, [filter.Policy.Field]) read once. It runs the checks
// of the general path in its order: the literal against the field's kind and enum values, then the field's visit, then the
// literal's.
func (e *evaluation) compareField(op filter.Operator, name string, f filter.FieldPolicy, lit *filter.LiteralNode) (any, error) {
	path := f.Path()
	if err := f.CheckLiteral(lit); err != nil {
		return nil, err
	}

	if err := e.Op(); err != nil {
		return nil, err
	}

	if !f.Allowed() {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", name)
	}

	lv := e.ReadPath(path)
	if err := e.Err(); err != nil {
		return nil, err
	}

	if err := e.Op(); err != nil {
		return nil, err
	}

	if e.config.FieldKind(name) == filter.FieldKindDecimal {
		return e.compareDecimalField(op, lv, lit, lit.Value)
	}

	rv := e.comparable(lit.Value)
	lv = e.comparable(lv)

	r := exactLiteral(lv, lit, rv)
	if !e.budget.chargeComparison(lv, r) {
		return nil, e.Err()
	}

	return e.compare(op, lv, r)
}

// compareDecimalField compares lv, the data value of a [filter.FieldKindDecimal] field, with rv, produced by right, exactly: both
// as decimals ([evaluation.decimalData], [evaluation.decimalOperand]).
func (e *evaluation) compareDecimalField(op filter.Operator, lv any, right filter.Node, rv any) (any, error) {
	l := e.decimalData(lv)
	if err := e.Err(); err != nil {
		return nil, err
	}

	if _, exact := l.(decimal.Decimal); !exact && l != nil {
		// A data value that is no decimal (a bool, a NaN, a string that is no number) compares as it always has.
		l, rv = e.comparable(lv), e.comparable(rv)

		r := exactLiteral(l, right, rv)
		if !e.budget.chargeComparison(l, r) {
			return nil, e.Err()
		}

		return e.compare(op, l, r)
	}

	if ident, ok := right.(*filter.IdentNode); ok && ident != nil && e.config.FieldKind(ident.Name) != filter.FieldKindDecimal {
		rv = e.comparable(rv) // a field of another kind keeps its own form
	} else {
		var err error
		if rv, err = e.decimalOperand(right, rv); err != nil {
			return nil, err
		}
	}

	if !e.budget.chargeComparison(l, rv) {
		return nil, e.Err()
	}

	return e.compare(op, l, rv)
}

// compareElements compares the elements of an array field ([filter.WithArrayFields]) with rv, produced by right: != holds when no
// element is equal, every other operator when one element satisfies it. A null element satisfies no ordering comparison, as in
// a MongoDB query. The first failed element comparison is the error.
func (e *evaluation) compareElements(op filter.Operator, elems []any, right filter.Node, rv any) (bool, error) {
	match := op
	if op == filter.OpNotEqual {
		match = filter.OpEqual
	}

	for _, el := range elems {
		if el == nil && !op.IsEquality() {
			continue
		}

		operand := exactLiteral(el, right, rv)
		if !e.budget.chargeComparison(el, operand) {
			return false, e.Err()
		}

		ok, err := e.compare(match, el, operand)
		if err != nil {
			return false, err
		}

		if ok {
			return op != filter.OpNotEqual, nil
		}
	}

	return op == filter.OpNotEqual, nil
}

// isArrayField reports whether n names a field declared by [filter.WithArrayFields].
func (e *evaluation) isArrayField(n filter.Node) bool {
	ident, ok := n.(*filter.IdentNode)
	return ok && e.config.IsArrayField(ident.Name)
}

// arrayElements returns the values a predicate on n tests when n names a field declared by [filter.WithArrayFields] (ok), as a MongoDB
// query traverses its path: a list at the end of the path yields its elements, a list of documents on the way the values found in
// each document — a list there yielding its elements too — and null for a document that lacks the rest of the path; an absent
// path yields null. An empty list, or one without documents on the way, yields nothing. size() measures the value
// [evaluation.VisitIdent] resolves instead, as an aggregation path does.
func (e *evaluation) arrayElements(n filter.Node) (elems []any, ok bool, err error) {
	ident, isIdent := n.(*filter.IdentNode)
	if !isIdent || !e.config.IsArrayField(ident.Name) {
		return nil, false, nil
	}

	if err := e.Op(); err != nil {
		return nil, false, err
	}

	if !e.config.IsFieldAllowed(ident.Name) {
		return nil, false, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", ident.Name)
	}

	elems = e.QueryValues(e.data, e.config.ApplyFieldMapping(ident.Name), true, nil)
	if err := e.Err(); err != nil {
		return nil, false, err
	}

	return elems, true, nil
}

// evalIn checks if the left value is in the right list. The schema
// check uses CheckLiteralKind directly because `in` has a fixed shape
// (ident on the left, list on the right) — the symmetric
// CheckComparison would also work but the asymmetry is intentional.
// `x in field` on a field declared by [filter.WithArrayFields] is the
// comparison field == x: x is checked against the field's declared
// kind, and an absent or null array contains nothing.
func (e *evaluation) evalIn(left, right filter.Node) (any, error) {
	if field, ok := right.(*filter.IdentNode); ok && e.config.IsArrayField(field.Name) && !filter.IsFieldReference(left) {
		return e.evalComparison(filter.OpEqual, field, left)
	}

	if ident, ok := left.(*filter.IdentNode); ok {
		if err := e.config.CheckLiteralKind(ident.Name, right); err != nil {
			return nil, err
		}
	}

	elems, elementwise, err := e.arrayElements(left)
	if err != nil {
		return nil, err
	}

	var lv any
	if !elementwise {
		if lv, err = left.Accept(e); err != nil {
			return nil, err
		}
	}

	dec := e.isDecimalField(left)
	if dec {
		lv = e.decimalData(lv)
		for i, el := range elems {
			elems[i] = e.decimalData(el)
		}
	}

	if ln, ok := right.(*filter.ListNode); ok {
		return e.evalInList(lv, elems, elementwise, dec, ln)
	}

	rv, err := right.Accept(e)
	if err != nil {
		return nil, err
	}

	list, ok := rv.([]any)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "in requires a list on the right side")
	}
	// The elements of a data list, each charged before it is read: the comparisons member charges do not cover an element when
	// there are none (an empty array field). An element compared with the elements of an array field is read in its stored form, as
	// they are; otherwise as the Go value it is, as lv is.
	for _, item := range list {
		if !e.budget.charge(1) {
			return true, e.Err()
		}

		if elementwise {
			item = e.Stored(item)
		}

		if dec {
			item = e.decimalData(item)
		}

		matched, err := e.member(lv, elems, elementwise, nil, item)
		if err != nil {
			return nil, err
		}

		if matched {
			return true, e.Err()
		}
	}

	return false, e.Err()
}

// member reports whether lv equals item, produced by node — or, for an array field (elementwise), whether one of its elems does.
// Each comparison is charged to the data budget; once it runs out, member reports true to stop the caller's scan, which then fails.
// lv and item are Go values as the data holds them: two values of Go types the evaluator does not read as they are ([plainType])
// are equal as Go's == finds them ([sameValue]).
func (e *evaluation) member(lv any, elems []any, elementwise bool, node filter.Node, item any) (bool, error) {
	if !elementwise {
		elems = []any{lv}
	}

	for _, el := range elems {
		operand := exactLiteral(el, node, item)
		if !e.budget.chargeComparison(el, operand) {
			return true, e.Err()
		}

		matched, err := e.memberValue(el, operand, elementwise)
		if err != nil || matched {
			return matched, err
		}
	}

	return false, nil
}

func (e *evaluation) memberValue(el, operand any, elementwise bool) (bool, error) {
	_, leftDecimal := el.(decimal.Decimal)

	_, rightDecimal := operand.(decimal.Decimal)
	if leftDecimal || rightDecimal {
		matched, err := e.compare(filter.OpEqual, el, operand)
		if err != nil || matched {
			return matched, err
		}

		return false, nil
	}

	if e.config.comparator != nil {
		matched, handled, err := e.config.comparator.Compare(filter.OpEqual, el, operand)
		if err != nil {
			return false, err
		}

		if handled {
			if matched {
				return true, nil
			}

			return false, nil
		}
	}

	if elementwise || plainType(el) || plainType(operand) {
		if valuesEqual(e.budget.enc, el, operand) {
			return true, nil
		}
	} else {
		equal, ok := sameValue(el, operand, &e.budget)
		if equal || !ok {
			return true, e.Err()
		}
	}

	return false, nil
}

// evalInList is [evaluation.VisitList] fused with the membership test: it
// charges the same operation and depth budget and evaluates every element in
// order, so a later element's error still surfaces after a match, but it does
// not materialize the []any.
// dec reports a [filter.FieldKindDecimal] field, whose elements are compared as exact decimals ([evaluation.decimalOperand]).
func (e *evaluation) evalInList(lv any, elems []any, elementwise, dec bool, n *filter.ListNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	found := false

	for _, elem := range n.Elements {
		val, err := elem.Accept(e)
		if err != nil {
			return nil, err
		}

		if dec {
			if val, err = e.decimalOperand(elem, val); err != nil {
				return nil, err
			}
		}

		if !found {
			var err error

			found, err = e.member(lv, elems, elementwise, elem, val)
			if err != nil {
				return nil, err
			}
		}
	}

	return found, e.Err()
}

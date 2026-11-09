// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateLogical handles && and || operators.
func (t *FilterTranslator) translateLogical(mongoOp string, left, right filter.Node) (bson.M, error) {
	leftM, err := t.acceptPredicate(left)
	if err != nil {
		return nil, err
	}

	rightM, err := t.acceptPredicate(right)
	if err != nil {
		return nil, err
	}

	return bson.M{mongoOp: bson.A{leftM, rightM}}, nil
}

// translateNot handles the ! operator.
func (t *FilterTranslator) translateNot(operand filter.Node) (bson.M, error) {
	if in, ok := operand.(*filter.BinaryOpNode); ok && in.Op == filter.OpIn {
		if ident, ok := in.Left.(*filter.IdentNode); ok && t.semantics.IsPresenceField(ident.Name) {
			return t.translatePresenceNotIn(ident, in.Right)
		}
	}
	// A negated string function over a Presence field is unknown on null: only a string value that fails the predicate matches,
	// whatever form (bson.Regex or $regex) the positive predicate takes.
	if call, ok := operand.(*filter.CallNode); ok && call.Op.IsStringOp() && t.isPresenceStringCall(call) {
		return t.translatePresenceNotString(call)
	}
	// For simple identifiers (like !active), treat as field != true
	if _, ok := operand.(*filter.IdentNode); ok {
		field, err := t.getFieldName(operand)
		if err != nil {
			return nil, err
		}

		return bson.M{field: bson.M{opNe: true}}, nil
	}

	// For complex expressions, use $nor
	inner, err := t.acceptPredicate(operand)
	if err != nil {
		return nil, err
	}

	return bson.M{opNor: bson.A{inner}}, nil
}

// translatePresenceNotIn translates `!(ident in right)` over a presence field: a null field is unknown, so it matches neither form.
func (t *FilterTranslator) translatePresenceNotIn(ident *filter.IdentNode, right filter.Node) (bson.M, error) {
	field, err := t.getFieldName(ident)
	if err != nil {
		return nil, err
	}

	positive, err := t.translateIn(ident, right)
	if err != nil {
		return nil, err
	}

	// A disjunction with field members.
	if list, isList := right.(*filter.ListNode); isList && hasDynamicElement(list) {
		return bson.M{opAnd: bson.A{bson.M{field: bson.M{opNe: nil}}, bson.M{opNor: bson.A{positive}}}}, nil
	}

	condition, ok := positive[field].(bson.M)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "in operator requires a field list predicate")
	}

	items, ok := condition["$in"].([]any)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "in operator requires a list")
	}

	return bson.M{field: bson.M{"$exists": true, "$nin": bson.A(append(items, nil))}}, nil
}

// translatePresenceNotString translates a negated string function reading a presence field: the target, and a presence field
// argument, must hold strings.
func (t *FilterTranslator) translatePresenceNotString(call *filter.CallNode) (bson.M, error) {
	positive, err := t.acceptPredicate(call)
	if err != nil {
		return nil, err
	}

	guards := bson.A{}

	for _, node := range append([]filter.Node{call.Target}, call.Args...) {
		ident, isIdent := node.(*filter.IdentNode)
		if !isIdent || (node != call.Target && !t.semantics.IsPresenceField(ident.Name)) {
			continue
		}

		field, err := t.getFieldName(ident)
		if err != nil {
			return nil, err
		}

		guards = append(guards, bson.M{field: bson.M{"$type": typeString}})
	}

	return bson.M{opAnd: append(guards, bson.M{opNor: bson.A{positive}})}, nil
}

// translateIn handles the in operator.
func (t *FilterTranslator) translateIn(left, right filter.Node) (bson.M, error) {
	// `x in tags` on an array field is the membership test MongoDB spells as the equality {tags: x}; a field or substring() x
	// tests it under $expr.
	if t.isArrayField(right) && (!filter.IsFieldReference(left) || t.isScalarField(left)) {
		return t.translateComparison(filter.OpEqual, right, left)
	}

	// A list holding a field, or a substring() on the left, is a disjunction of equalities.
	if list, ok := right.(*filter.ListNode); ok && (hasDynamicElement(list) || isSubstring(left)) {
		return t.translateDynamicIn(left, list)
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return nil, err
	}

	if ident, ok := left.(*filter.IdentNode); ok {
		if err = t.config.CheckLiteralKind(ident.Name, right); err != nil {
			return nil, err
		}
	}

	values, err := right.Accept(visitor{t})
	if err != nil {
		return nil, err
	}

	valuesSlice, ok := values.([]any)
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "in operator requires a list, got %T", values)
	}

	emitted := valuesSlice

	if t.isDecimalField(left) {
		var elems []filter.Node
		if list, isList := right.(*filter.ListNode); isList && len(list.Elements) == len(valuesSlice) {
			elems = list.Elements
		}

		emitted = make([]any, len(valuesSlice))
		for i, v := range valuesSlice {
			var elem filter.Node
			if elems != nil {
				elem = elems[i]
			}

			if emitted[i], err = t.decimalValue(left, elem, v); err != nil {
				return nil, err
			}
		}
	}

	base := bson.M{field: bson.M{"$in": emitted}}
	if kind, declared := t.zeroKind(left); declared {
		return absentAs(field, zeroMatchesIn(kind, emitted), base), nil
	}

	return base, nil
}

// isPresenceStringCall reports whether a string function reads a presence field, as its target or its field argument.
func (t *FilterTranslator) isPresenceStringCall(call *filter.CallNode) bool {
	if t.semantics.IsPresenceCall(call) {
		return true
	}

	for _, arg := range call.Args {
		if ident, ok := arg.(*filter.IdentNode); ok && t.semantics.IsPresenceField(ident.Name) {
			return true
		}
	}

	return false
}

// isScalarField reports whether node is a field not declared by [filter.WithArrayFields].
func (t *FilterTranslator) isScalarField(node filter.Node) bool {
	_, ok := node.(*filter.IdentNode)
	return ok && !t.isArrayField(node)
}

// translateRegexOp handles string functions like contains, startsWith, endsWith, matches.
func (t *FilterTranslator) translateRegexOp(target filter.Node, args []filter.Node, transform regexTransform, matches bool) (bson.M, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return nil, err
	}

	if len(args) != 1 {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "string function requires exactly 1 argument")
	}

	arg, err := filter.AcceptLiteral(visitor{t}, args[0])
	if err != nil {
		return nil, err
	}

	pattern, ok := arg.(string)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "string function argument must be a string")
	}

	regexPattern, err := transform(pattern)
	if err != nil {
		return nil, err
	}

	base := bson.M{field: bson.M{"$regex": regexPattern}}

	kind, declared := t.zeroKind(target)
	if !declared {
		return base, nil
	}

	zeroMatches := kind == filter.FieldKindString && pattern == ""
	if matches {
		zeroMatches, err = t.zeroMatchesRegex(kind, pattern)
		if err != nil {
			return nil, err
		}
	}

	return absentAs(field, zeroMatches, base), nil
}

// translateSize rejects a size() call reached outside a comparison: a length is an integer, not a predicate, so there is nothing for
// the query to test — as the SQL translator, which turns the same expression away. translateComparison handles size() on the left
// of a comparison without visiting the call.
func translateSize(target filter.Node) error {
	if target == nil {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
	}

	return coreerrs.Wrap(filter.ErrUnsupportedOperation, "size() outside a comparison")
}

// translateHas handles the has() function.
func (t *FilterTranslator) translateHas(target filter.Node) (bson.M, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return nil, err
	}

	if ident, ok := target.(*filter.IdentNode); ok && t.semantics.IsPresenceField(ident.Name) {
		return bson.M{field: bson.M{opNe: nil}}, nil
	}

	if ident, ok := target.(*filter.IdentNode); ok && t.config.PortableContract() && t.config.IsArrayField(ident.Name) {
		return bson.M{field + ".0": bson.M{opExists: true}}, nil
	}

	if kind, declared := t.zeroKind(target); declared {
		zero := &filter.LiteralNode{Value: zeroValue(kind)}
		return t.translateComparison(filter.OpNotEqual, target, zero)
	}

	return bson.M{field: bson.M{opExists: true}}, nil
}

// getFieldName extracts the field name from a node.
func (t *FilterTranslator) getFieldName(node filter.Node) (string, error) {
	if err := filter.CheckFieldReference(node); err != nil {
		return "", err
	}

	// A size() call is a field reference only as the left side of a comparison, which translateSizeComparison handles; as the
	// target of in, a string function, has() or another size() it would stand for its field and test the field's value.
	if call, isCall := node.(*filter.CallNode); isCall {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v in field position", call.Op)
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	field, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field name, got %T", result)
	}

	return field, nil
}

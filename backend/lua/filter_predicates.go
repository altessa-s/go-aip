// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// utf8CharPattern matches one UTF-8 encoded character in a Lua 5.1 pattern (no utf8 library): a leading byte and its
// continuation bytes.
const utf8CharPattern = `"[%z\1-\127\194-\244][\128-\191]*"`

// utf8LeadPattern matches the leading byte of a UTF-8 character; counting its matches counts code points.
const utf8LeadPattern = `"[%z\1-\127\194-\244]"`

// substringFunc returns the code points [i, j) of s (j = nil: to the end), or nil when s is not a string or the bounds are not
// integers within 0 <= i <= j <= length, the bounds the evaluator accepts.
var substringFunc = `(function(s, i, j) if type(s) ~= "string" or type(i) ~= "number" or math.floor(i) ~= i or ` +
	`(j ~= nil and (type(j) ~= "number" or math.floor(j) ~= j)) then return nil end ` +
	`local n, o = 0, {} for c in string.gmatch(s, ` + utf8CharPattern + `) do ` +
	`if n >= i and (j == nil or n < j) then o[#o + 1] = c end n = n + 1 end ` +
	`if i < 0 or i > n or (j ~= nil and (j < i or j > n)) then return nil end return table.concat(o) end)`

// predicate renders a test of one value v against an optional operand x (both Lua expressions).
type predicate func(v, x string) string

// anyValue renders pred over the field at ref. A field declared by [filter.WithArrayFields] is tested element by element, as the
// evaluator and MongoDB read it: the test holds when one element satisfies pred; a value that is not a table (absent, null or a
// scalar) is tested itself. x is passed once as the second argument, so a field operand is read once; "" means pred takes none.
// Any other field is tested inline.
func anyValue(ref string, array bool, x string, pred predicate) string {
	if !array {
		return pred(ref, x)
	}

	params, args := "v", ref
	if x != "" {
		params, args, x = "v, x", ref+", "+x, "x"
	}

	return fmt.Sprintf(`(function(%s) if type(v) ~= "table" then return %s end for _, e in ipairs(v) do if %s then return true end end return false end)(%s)`,
		params, pred("v", x), pred("e", x), args)
}

// isArrayField reports whether node names a field declared by [filter.WithArrayFields].
func (t *FilterTranslator) isArrayField(node filter.Node) bool {
	ident, ok := node.(*filter.IdentNode)
	return ok && t.config.IsArrayField(ident.Name)
}

// translateComparison handles comparison operators (==, !=, <, >, <=, >=), or renders the negation of the comparison when
// negated ([FilterTranslator.negate]).
func (t *FilterTranslator) translateComparison(op filter.Operator, left, right filter.Node, negated bool) (string, error) {
	// Check if left side is a size() call
	if call, ok := left.(*filter.CallNode); ok && call.Op == filter.OpSize {
		return t.translateSizeComparison(op, call, right, negated)
	}

	if isSubstring(left) || isSubstring(right) {
		return t.translateSubstringComparison(op, left, right, negated)
	}

	// Type-check first: a mirrored `"lit" == field` is a type mismatch, not
	// a malformed field reference.
	if err := t.config.CheckComparison(left, right); err != nil {
		return "", err
	}

	if err := checkKinds(t.operandKind(left), t.operandKind(right)); err != nil {
		return "", err
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return "", err
	}

	right, err = t.config.NormalizeDecimalOperand(left, right)
	if err != nil {
		return "", err
	}

	rightStr, err := t.valueString(right)
	if err != nil {
		return "", err
	}

	luaOp, err := t.luaOperator(op)
	if err != nil {
		return "", err
	}

	array := t.isArrayField(left)
	ref := t.fieldRef(field)

	// An array field: != holds when no element is equal, every other operator when one element satisfies it. Each is true or
	// false, never unknown, so the negation is its complement.
	if op == filter.OpNotEqual && array {
		return complement("(not "+anyValue(ref, true, rightStr, equalTo)+")", negated), nil
	}

	if op.IsEquality() {
		if !array {
			if negated {
				luaOp = map[string]string{"==": "~=", "~=": "=="}[luaOp]
			}

			if luaOp == "~=" {
				_, literal := right.(*filter.LiteralNode)
				return notEqual(ref, rightStr, literal), nil
			}

			return fmt.Sprintf("(%s %s %s)", ref, luaOp, rightStr), nil
		}

		return complement(anyValue(ref, true, rightStr, equalTo), negated), nil
	}

	pred, err := orderedPredicate(luaOp, right, rightStr, negated && !array)
	if err != nil {
		return "", err
	}

	if !array || !negated {
		return anyValue(ref, array, rightStr, pred), nil
	}

	// No element orders against a nil field operand: the comparison is unknown unless there is no element to compare, as in
	// the evaluator, so its negation needs the operand or no element.
	some := anyValue(ref, true, rightStr, pred)
	if _, literal := right.(*filter.LiteralNode); literal {
		return "(not " + some + ")", nil
	}

	return fmt.Sprintf(`((%s ~= nil or %s == nil or (type(%s) == "table" and next(%s) == nil)) and not %s)`,
		rightStr, ref, ref, ref, some), nil
}

// notEqual renders the scalar inequality v != x with the evaluator's semantics. Null equals only null, so a nil operand makes
// it hold unless both are nil; two values of different types, or two tables, do not compare (the evaluator finds the
// comparison unknown or fails the record), so neither the inequality nor the equality matches them. Lua's own ~= is true for
// any two values of different types, which would match a string field against != 1. literal reports that x renders a
// filter literal, never a table and nil only as the null literal.
func notEqual(v, x string, literal bool) string {
	switch {
	case x == luaNil && literal:
		return fmt.Sprintf("(%s ~= nil)", v)
	case literal:
		return fmt.Sprintf(`(%s == nil or (type(%s) == "%s" and %s ~= %s))`, v, v, literalType(x), v, x)
	default:
		return fmt.Sprintf(`((%s == nil) ~= (%s == nil) or (type(%s) == type(%s) and type(%s) ~= "table" and %s ~= %s))`,
			v, x, v, x, v, v, x)
	}
}

// literalType returns the Lua type of a rendered non-nil scalar literal: a quoted string (text, bytes), a boolean, or a number
// (integers, floats and timestamps).
func literalType(x string) string {
	switch {
	case strings.HasPrefix(x, `"`):
		return luaTypeString
	case x == luaTrue || x == luaFalse:
		return luaTypeBoolean
	default:
		return luaTypeNumber
	}
}

// complement renders the negation of the two-valued predicate expr when negated, expr otherwise.
func complement(expr string, negated bool) string {
	if !negated {
		return expr
	}

	return "(not " + expr + ")"
}

// orderedPredicate renders the ordered comparison v luaOp x so that it never raises a Lua runtime error, which would abort the
// whole script: a value Lua cannot order against x (nil, a boolean, a table, a value of another type) is no match, as the
// evaluator finds the comparison unknown or fails that one record. x is the rendering of operand: against a literal the guard
// is the literal's Lua type; against a field both values must be numbers or both strings. negated renders the negation, which
// holds behind the same guard: where the comparison is unknown, so is its negation. An ordered comparison with null or a
// boolean literal is an unsupported operation (the evaluator orders neither).
func orderedPredicate(luaOp string, operand filter.Node, x string, negated bool) (predicate, error) {
	var litType string

	if _, ok := operand.(*filter.LiteralNode); ok {
		switch {
		case x == luaNil:
			return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %s with null", luaOp)
		case x == luaTrue || x == luaFalse:
			return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %s of booleans", luaOp)
		case strings.HasPrefix(x, `"`):
			litType = luaTypeString
		default:
			litType = luaTypeNumber
		}
	}

	return func(v, x string) string {
		cmp := fmt.Sprintf("%s %s %s", v, luaOp, x)
		if negated {
			cmp = "not (" + cmp + ")"
		}

		if litType != "" {
			return fmt.Sprintf(`(type(%s) == "%s" and %s)`, v, litType, cmp)
		}

		return fmt.Sprintf(`(type(%s) == type(%s) and (type(%s) == "number" or type(%s) == "string") and %s)`, v, x, v, v, cmp)
	}, nil
}

// operandKind is the declared kind of a comparison operand: the declared kind of a field, int for size(), string for
// substring(), unspecified for anything else (a literal is checked by [filter.Policy.CheckComparison]).
func (t *FilterTranslator) operandKind(n filter.Node) filter.FieldKind {
	switch x := n.(type) {
	case *filter.IdentNode:
		return t.config.FieldKind(x.Name)
	case *filter.CallNode:
		switch x.Op {
		case filter.OpSize:
			return filter.FieldKindInt
		case filter.OpSubstring:
			return filter.FieldKindString
		default:
			return filter.FieldKindUnspecified
		}
	default:
		return filter.FieldKindUnspecified
	}
}

// checkKinds rejects two operands whose declared kinds can never compare equal, as the MongoDB translator does; an undeclared
// kind is not checked.
func checkKinds(a, b filter.FieldKind) error {
	if filter.KindsComparable(a, b) {
		return nil
	}

	return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "cannot compare %s with %s", a, b)
}

// equalTo is the predicate v == x.
func equalTo(v, x string) string { return fmt.Sprintf("(%s == %s)", v, x) }

// translateIn handles the in operator by expanding to an OR chain; negated renders its complement (membership is never
// unknown).
func (t *FilterTranslator) translateIn(left, right filter.Node, negated bool) (string, error) {
	// `x in tags` on an array field is the membership test tags == x, as in the evaluator and the MongoDB translator.
	if t.isArrayField(right) && !filter.IsFieldReference(left) {
		return t.translateComparison(filter.OpEqual, right, left, negated)
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

	if list, ok := right.(*filter.ListNode); ok {
		for _, el := range list.Elements {
			if err = checkKinds(t.operandKind(left), t.operandKind(el)); err != nil {
				return "", err
			}
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

	// Nothing is a member of an empty list. Joining no comparisons would emit
	// `()`, which is not a Lua expression.
	if len(valuesSlice) == 0 {
		if negated {
			return luaTrue, nil
		}

		return luaFalse, nil
	}

	members := make([]string, 0, len(valuesSlice))
	for _, v := range valuesSlice {
		s, ok := v.(string)
		if !ok {
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected literal value in list, got %T", v)
		}

		members = append(members, s)
	}

	pred := func(v, _ string) string {
		parts := make([]string, len(members))
		for i, m := range members {
			parts[i] = fmt.Sprintf("%s == %s", v, m)
		}

		return "(" + strings.Join(parts, " or ") + ")"
	}

	return complement(anyValue(t.fieldRef(field), t.isArrayField(left), "", pred), negated), nil
}

// stringArg renders the single argument of contains/startsWith/endsWith: a string literal (lit holds its value) or a field
// reference (lit is nil). A literal of another type or a wrong arity is a malformed expression, as in the evaluator; any other
// operand (a nested call) is an unsupported operation, as in every other translator.
func (t *FilterTranslator) stringArg(args []filter.Node) (arg string, lit *string, err error) {
	if len(args) != 1 {
		return "", nil, coreerrs.Wrap(filter.ErrInvalidExpression, "string function requires exactly 1 argument")
	}

	switch a := args[0].(type) {
	case *filter.LiteralNode:
		s, ok := a.Value.(string)
		if !ok {
			return "", nil, coreerrs.Wrap(filter.ErrInvalidExpression, "string function argument must be a string")
		}

		return quoteString(s), &s, nil
	case *filter.IdentNode:
		ref, err := t.valueString(a)
		return ref, nil, err
	default:
		return "", nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "string function argument %T", args[0])
	}
}

// stringBody renders the test of the string v against the string x; lit is the argument's value when it is a literal, so the
// rendering can use its byte length as a constant.
type stringBody func(v, x string, lit *string) string

// translateStringCall handles contains/startsWith/endsWith. The test holds only for a string value (and a string argument),
// so an absent, null or numeric field is false, as in the evaluator, instead of a Lua error or a number-to-string coercion;
// an array field is tested element by element. negated renders the negation: it holds for a target that is not a string, but
// not where a field argument is not a string, for which the evaluator finds the test unknown (nil) or fails.
func (t *FilterTranslator) translateStringCall(target filter.Node, args []filter.Node, body stringBody, negated bool) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	arg, lit, err := t.stringArg(args)
	if err != nil {
		return "", err
	}

	pred := func(v, x string) string {
		if lit != nil {
			return fmt.Sprintf(`(type(%s) == "string" and %s)`, v, body(v, x, lit))
		}

		return fmt.Sprintf(`(type(%s) == "string" and type(%s) == "string" and %s)`, v, x, body(v, x, nil))
	}

	ref, array := t.fieldRef(field), t.isArrayField(target)
	test := anyValue(ref, array, arg, pred)

	switch {
	case !negated:
		return test, nil
	case lit != nil:
		return "(not " + test + ")", nil
	case array:
		return fmt.Sprintf(`(type(%s) == "string" and not %s)`, arg, test), nil
	default:
		return fmt.Sprintf(`(type(%s) ~= "string" or (type(%s) == "string" and not %s))`, ref, arg, test), nil
	}
}

// translateContains handles field.contains(sub): a plain (not pattern) find.
func (t *FilterTranslator) translateContains(target filter.Node, args []filter.Node, negated bool) (string, error) {
	return t.translateStringCall(target, args, func(v, x string, _ *string) string {
		return fmt.Sprintf("string.find(%s, %s, 1, true) ~= nil", v, x)
	}, negated)
}

// translateStartsWith handles field.startsWith(pre).
func (t *FilterTranslator) translateStartsWith(target filter.Node, args []filter.Node, negated bool) (string, error) {
	return t.translateStringCall(target, args, func(v, x string, lit *string) string {
		if lit != nil {
			return fmt.Sprintf("string.sub(%s, 1, %d) == %s", v, len(*lit), x)
		}

		return fmt.Sprintf("string.sub(%s, 1, #%s) == %s", v, x, x)
	}, negated)
}

// translateEndsWith handles field.endsWith(suf). An empty suffix gets its own form: string.sub(s, -0) is the whole string,
// not an empty tail, while string.sub(s, 1, 0) is "" for any string.
func (t *FilterTranslator) translateEndsWith(target filter.Node, args []filter.Node, negated bool) (string, error) {
	return t.translateStringCall(target, args, func(v, x string, lit *string) string {
		switch {
		case lit == nil:
			return fmt.Sprintf("(#%s == 0 or string.sub(%s, -#%s) == %s)", x, v, x, x)
		case *lit == "":
			return fmt.Sprintf("string.sub(%s, 1, 0) == %s", v, x)
		default:
			return fmt.Sprintf("string.sub(%s, -%d) == %s", v, len(*lit), x)
		}
	}, negated)
}

// translateSizeComparison handles size() comparisons like tags.size() == 3: the UTF-8 code-point count of a string, the
// element count of a table, and for an array field 0 for an absent or null value (an absent array is empty, as in the
// evaluator). size() of any other field that holds no string or table is unknown, as in the evaluator, so neither the
// comparison nor its negation (negated) holds for it; nor does an ordered comparison with a field operand that is no number.
func (t *FilterTranslator) translateSizeComparison(op filter.Operator, call *filter.CallNode, right filter.Node, negated bool) (string, error) {
	field, err := t.getFieldName(call.Target)
	if err != nil {
		return "", err
	}

	if err = checkKinds(filter.FieldKindInt, t.operandKind(right)); err != nil {
		return "", err
	}

	rightStr, err := t.valueString(right)
	if err != nil {
		return "", err
	}

	luaOp, err := t.luaOperator(op)
	if err != nil {
		return "", err
	}

	// size() is a number: a literal of another type can never compare with it, and a field operand must hold a number at run
	// time for an ordered comparison, or Lua raises an error.
	var guard string

	if _, ok := right.(*filter.LiteralNode); ok {
		switch {
		case rightStr == luaNil && !op.IsEquality():
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %s with null", luaOp)
		case rightStr == luaTrue || rightStr == luaFalse || strings.HasPrefix(rightStr, `"`):
			return "", coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "size() compared with %s", rightStr)
		}
	} else if !op.IsEquality() {
		guard = fmt.Sprintf(`type(%s) == "number" and `, rightStr)
	}

	ref := t.fieldRef(field)
	// Count UTF-8 leading bytes on Lua 5.1, which has no utf8 library.
	size := fmt.Sprintf(`(type(%s) == "string" and select(2, string.gsub(%s, %s, "")) or type(%s) == "table" and #%s or 0)`,
		ref, ref, utf8LeadPattern, ref, ref)

	if !t.isArrayField(call.Target) {
		guard = fmt.Sprintf(`(type(%s) == "string" or type(%s) == "table") and `, ref, ref) + guard
		size = fmt.Sprintf(`(type(%s) == "string" and select(2, string.gsub(%s, %s, "")) or #%s)`, ref, ref, utf8LeadPattern, ref)
	}

	cmp := fmt.Sprintf("%s %s %s", size, luaOp, rightStr)
	if negated {
		cmp = "not (" + cmp + ")"
	}

	return fmt.Sprintf("(%s%s)", guard, cmp), nil
}

// translateHas handles has(field) → field ~= nil, negated field == nil.
func (t *FilterTranslator) translateHas(target filter.Node, negated bool) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	if negated {
		return fmt.Sprintf("(%s == nil)", t.fieldRef(field)), nil
	}

	return fmt.Sprintf("(%s ~= nil)", t.fieldRef(field)), nil
}

// isSubstring reports whether n is a substring() call.
func isSubstring(n filter.Node) bool {
	call, ok := n.(*filter.CallNode)
	return ok && call.Op == filter.OpSubstring
}

// operand renders one side of a substring comparison and reports whether it can be nil because it is a substring() result.
func (t *FilterTranslator) operand(n filter.Node) (expr string, nilable bool, err error) {
	if !isSubstring(n) {
		expr, err = t.valueString(n)
		return expr, false, err
	}

	call, _ := n.(*filter.CallNode) // isSubstring checked the type

	expr, err = t.substring(call)

	return expr, true, err
}

// translateSubstringComparison handles a comparison with a substring() operand. A substring that does not exist (a target that
// is not a string, bounds out of range) makes the comparison false whatever the operator, where the evaluator finds it unknown
// or fails the evaluation: a server-side script cannot report a per-record error. An ordered comparison of different types is
// false too. negated renders the negation, false in the same cases.
func (t *FilterTranslator) translateSubstringComparison(op filter.Operator, left, right filter.Node, negated bool) (string, error) {
	luaOp, err := t.luaOperator(op)
	if err != nil {
		return "", err
	}

	l, lNil, err := t.operand(left)
	if err != nil {
		return "", err
	}

	r, rNil, err := t.operand(right)
	if err != nil {
		return "", err
	}

	if err = checkKinds(t.operandKind(left), t.operandKind(right)); err != nil {
		return "", err
	}

	if !op.IsEquality() && (l == luaNil || r == luaNil) {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %s with null", luaOp)
	}

	var guards []string
	if lNil {
		guards = append(guards, "a == nil")
	}

	if rNil {
		guards = append(guards, "b == nil")
	}

	cmp := "a " + luaOp + " b"
	if negated {
		cmp = "not (" + cmp + ")"
	}

	if !op.IsEquality() {
		cmp = "type(a) == type(b) and " + cmp
	}

	return fmt.Sprintf("(function(a, b) if %s then return false end return %s end)(%s, %s)",
		strings.Join(guards, " or "), cmp, l, r), nil
}

// substring renders target.substring(start[, end]) as a call of [substringFunc]. A bound is an integer literal or a field
// reference; a literal bound that is not an integer, negative, or past the end bound is a malformed expression, as in the
// evaluator.
func (t *FilterTranslator) substring(call *filter.CallNode) (string, error) {
	const (
		minArgs = 1
		maxArgs = 2
	)

	field, err := t.getFieldName(call.Target)
	if err != nil {
		return "", err
	}

	if ident, ok := call.Target.(*filter.IdentNode); ok {
		if kind := t.config.FieldKind(ident.Name); kind != filter.FieldKindUnspecified && kind != filter.FieldKindString {
			return "", coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "substring() of field %q (%s)", ident.Name, kind)
		}
	}

	if len(call.Args) < minArgs || len(call.Args) > maxArgs {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	bounds := []string{luaNil, luaNil}
	literals := make([]int64, 0, maxArgs)

	for i, arg := range call.Args {
		switch a := arg.(type) {
		case *filter.LiteralNode:
			n, ok := a.Value.(int64)
			if !ok {
				return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() argument must be int, got %T", a.Value)
			}

			if n < 0 {
				return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() argument %d is negative", n)
			}

			literals = append(literals, n)
			bounds[i] = strconv.FormatInt(n, 10)
		case *filter.IdentNode:
			if bounds[i], err = t.valueString(a); err != nil {
				return "", err
			}
		default:
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "substring() argument %T", arg)
		}
	}

	if len(literals) == maxArgs && literals[0] > literals[1] {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "substring(%d, %d): start is past end", literals[0], literals[1])
	}

	return fmt.Sprintf("%s(%s, %s, %s)", substringFunc, t.fieldRef(field), bounds[0], bounds[1]), nil
}

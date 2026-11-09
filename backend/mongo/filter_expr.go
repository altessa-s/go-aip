// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// This file translates the predicates a query document cannot express — a field compared with another field, a field
// argument of a string function, and substring() — into aggregation expressions under $expr. Their semantics:
//
//   - A field operand reads the value the aggregation path resolves to; missing and BSON null are the same null (a field
//     declared by [WithSchemaZeroWhenAbsent] reads its zero value instead). Values compare as whole values; only a
//     field declared by [filter.WithArrayFields] compared with a non-array operand matches element-wise, as `tags == "x"` does.
//   - == holds when both operands are equal (two nulls are equal, as `a == null` matches a null field); != is its negation.
//   - <, <=, >, >= hold only when both operands are non-null and of one type class (numbers across int, long, double and
//     decimal; otherwise the same BSON type), as a query comparison does.
//   - A size() or substring() operand that has no value (a value without a size; a non-string target or an out-of-range
//     substring) makes every comparison false, != included; so does a null [WithSchemaPresenceFields] field.
//   - Every literal is wrapped in $literal, so a string such as "$secret" is never read as a field path.

// Aggregation operators the expression forms emit.
const (
	opCond     = "$cond"
	opType     = "$type"
	opStrLenCP = "$strLenCP"
	opNot      = "$not"
	opOr       = "$or"
	opLiteral  = "$literal"
	opIn       = "$in"
	opIndexCP  = "$indexOfCP"
	opSubstrCP = "$substrCP"
	opIsNumber = "$isNumber"
	opIsArray  = "$isArray"
	opIfNull   = "$ifNull"
	opSubtract = "$subtract"
	typeString = "string"
)

// exprOperand is one operand of an expression predicate.
type exprOperand struct {
	expr     any              // the aggregation expression of the value
	kind     filter.FieldKind // the declared kind, FieldKindUnspecified when unknown
	array    bool             // a field declared by filter.WithArrayFields
	nullable bool             // the operand may have no value; every comparison is then false
	literal  bool             // a literal operand
	value    any              // the encoded value of a literal operand
	isNull   bool             // the null literal
}

// isSubstring reports whether node is a substring() call.
func isSubstring(node filter.Node) bool {
	call, ok := node.(*filter.CallNode)
	return ok && call != nil && call.Op == filter.OpSubstring
}

// needsExpr reports whether the comparison of left and right is translated to $expr.
func needsExpr(left, right filter.Node) bool {
	if isSubstring(left) || isSubstring(right) {
		return true
	}

	_, leftLit := left.(*filter.LiteralNode)
	_, rightLit := right.(*filter.LiteralNode)

	return !leftLit && !rightLit && filter.IsFieldReference(left) && filter.IsFieldReference(right)
}

// operand builds the expression of a comparison operand.
func (t *FilterTranslator) operand(node filter.Node) (exprOperand, error) {
	switch n := node.(type) {
	case *filter.LiteralNode:
		value, err := t.visitLiteral(n)
		if err != nil {
			return exprOperand{}, err
		}

		return exprOperand{expr: bson.M{opLiteral: value}, kind: literalKind(n.Value), literal: true, value: value, isNull: value == nil}, nil
	case *filter.IdentNode:
		return t.fieldOperand(n)
	case *filter.CallNode:
		switch n.Op {
		case filter.OpSize:
			return t.sizeOperand(n)
		case filter.OpSubstring:
			return t.substringOperand(n)
		default:
			return exprOperand{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v as a comparison operand", n.Op)
		}
	default:
		return exprOperand{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a comparison operand", node)
	}
}

// fieldOperand is the value of a field: null when missing, the declared zero under WithSchemaZeroWhenAbsent.
func (t *FilterTranslator) fieldOperand(ident *filter.IdentNode) (exprOperand, error) {
	field, err := t.getFieldName(ident)
	if err != nil {
		return exprOperand{}, err
	}

	var absent any
	if kind, declared := t.zeroKind(ident); declared {
		absent = zeroValue(kind)
	}

	return exprOperand{
		expr:     bson.M{opIfNull: bson.A{"$" + field, absent}},
		kind:     t.config.FieldKind(ident.Name),
		array:    t.config.IsArrayField(ident.Name),
		nullable: t.semantics.IsPresenceField(ident.Name),
	}, nil
}

// sizeOperand is the size of a field ([sizeLength]): absent and null count as an empty array; a value without a size has no value.
func (t *FilterTranslator) sizeOperand(call *filter.CallNode) (exprOperand, error) {
	if call.Target == nil {
		return exprOperand{}, coreerrs.Wrap(filter.ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
	}

	field, err := t.getFieldName(call.Target)
	if err != nil {
		return exprOperand{}, err
	}

	return exprOperand{expr: sizeLength(field), kind: filter.FieldKindInt, nullable: true}, nil
}

// substringOperand is `field.substring(start[, end])`: the code points [start, end) of a string, or no value when the field is
// not a string or end exceeds its length. The bounds must be integer literals with 0 <= start <= end.
func (t *FilterTranslator) substringOperand(call *filter.CallNode) (exprOperand, error) {
	ident, ok := call.Target.(*filter.IdentNode)
	if !ok {
		if err := filter.CheckFieldReference(call.Target); err != nil {
			return exprOperand{}, err
		}

		return exprOperand{}, coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() of a computed value")
	}

	if t.config.IsArrayField(ident.Name) {
		return exprOperand{}, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "substring() of array field %s", ident.Name)
	}

	if kind := t.config.FieldKind(ident.Name); kind != filter.FieldKindUnspecified && kind != filter.FieldKindString {
		return exprOperand{}, coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "substring() of field %q (%s)", ident.Name, kind)
	}

	start, end, hasEnd, err := substringBounds(call.Args)
	if err != nil {
		return exprOperand{}, err
	}

	value, err := t.fieldOperand(ident)
	if err != nil {
		return exprOperand{}, err
	}

	s := value.expr
	length := bson.M{opStrLenCP: s}

	// The code-point count to take: end-start, or the rest of the string.
	count := any(end - start)
	limit := end

	if !hasEnd {
		count = bson.M{opSubtract: bson.A{length, start}}
		limit = start
	}

	// $cond evaluates only the branch it takes, so $strLenCP and $substrCP never see a non-string.
	expr := bson.M{opCond: bson.A{
		bson.M{opEq: bson.A{bson.M{opType: s}, typeString}},
		bson.M{opCond: bson.A{
			bson.M{opGte: bson.A{length, limit}},
			bson.M{opSubstrCP: bson.A{s, start, count}},
			nil,
		}},
		nil,
	}}

	return exprOperand{expr: expr, kind: filter.FieldKindString, nullable: true}, nil
}

// substringBounds reads the integer literal arguments of substring().
func substringBounds(args []filter.Node) (start, end int64, hasEnd bool, err error) {
	if len(args) < 1 || len(args) > 2 {
		return 0, 0, false, coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	var bounds [2]int64

	for i, arg := range args {
		lit, ok := arg.(*filter.LiteralNode)
		if !ok || lit == nil {
			return 0, 0, false, coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() bounds must be integer literals")
		}

		v, ok := lit.Value.(int64)
		if !ok {
			return 0, 0, false, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bound must be an int, got %T", lit.Value)
		}

		if v < 0 || v > math.MaxInt32 {
			return 0, 0, false, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bound %d out of range", v)
		}

		bounds[i] = v
	}

	if len(args) == 1 {
		return bounds[0], 0, false, nil
	}

	if bounds[0] > bounds[1] {
		return 0, 0, false, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring(%d, %d): start after end", bounds[0], bounds[1])
	}

	return bounds[0], bounds[1], true, nil
}

// literalKind is the kind class of a literal value.
func literalKind(v any) filter.FieldKind {
	switch v.(type) {
	case int64, uint64:
		return filter.FieldKindInt
	case float64:
		return filter.FieldKindFloat
	case string:
		return filter.FieldKindString
	case bool:
		return filter.FieldKindBool
	case []byte:
		return filter.FieldKindBytes
	default:
		return filter.FieldKindUnspecified
	}
}

// checkKinds rejects two operands whose declared kinds can never compare equal.
func checkKinds(left, right exprOperand) error {
	a, b := left.kind, right.kind
	if filter.KindsComparable(a, b) {
		return nil
	}

	return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "cannot compare %s with %s", a, b)
}

// translateExprComparison translates `left op right` where an operand is a field compared with another field, a size() compared
// with a non-literal, or a substring() call.
func (t *FilterTranslator) translateExprComparison(op filter.Operator, left, right filter.Node) (bson.M, error) {
	l, err := t.operand(left)
	if err != nil {
		return nil, err
	}

	r, err := t.operand(right)
	if err != nil {
		return nil, err
	}

	if l.isNull || r.isNull {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison of a computed value with null")
	}

	if err = checkKinds(l, r); err != nil {
		return nil, err
	}

	// $expr compares BinData in the same length-first order as a query operator.
	if !op.IsEquality() && (l.kind == filter.FieldKindBytes || r.kind == filter.FieldKindBytes) {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v of bytes: MongoDB orders BinData by length first", op)
	}

	if err = t.checkSizeLiteral(left, r); err != nil {
		return nil, err
	}

	if err = t.checkSizeLiteral(right, l); err != nil {
		return nil, err
	}

	var guards bson.A

	for _, o := range []exprOperand{l, r} {
		if o.nullable {
			guards = append(guards, bson.M{opNe: bson.A{o.expr, nil}})
		}
	}

	core, err := comparisonExpr(op, l, r)
	if err != nil {
		return nil, err
	}

	if len(guards) == 0 {
		return bson.M{opExpr: core}, nil
	}

	return bson.M{opExpr: bson.M{opAnd: append(guards, core)}}, nil
}

// checkSizeLiteral checks a literal compared with a size() call.
func (t *FilterTranslator) checkSizeLiteral(node filter.Node, other exprOperand) error {
	call, ok := node.(*filter.CallNode)
	if !ok || call.Op != filter.OpSize || !other.literal {
		return nil
	}

	return CheckSizeOperand(fieldLabel(call.Target), other.value)
}

// comparisonExpr is the aggregation expression of `l op r`, without the nullable guards.
func comparisonExpr(op filter.Operator, l, r exprOperand) (any, error) {
	switch op {
	case filter.OpEqual:
		return equalityExpr(l, r), nil
	case filter.OpNotEqual:
		return bson.M{opNot: bson.A{equalityExpr(l, r)}}, nil
	case filter.OpLT, filter.OpLTE, filter.OpGT, filter.OpGTE:
		if l.array || r.array {
			return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordering comparison %v of an array field with a computed value", op)
		}

		mongoOp := orderingOps[op]

		return bson.M{opAnd: bson.A{
			bson.M{opNe: bson.A{l.expr, nil}},
			bson.M{opNe: bson.A{r.expr, nil}},
			bson.M{opOr: bson.A{
				bson.M{opAnd: bson.A{bson.M{opIsNumber: l.expr}, bson.M{opIsNumber: r.expr}}},
				bson.M{opEq: bson.A{bson.M{opType: l.expr}, bson.M{opType: r.expr}}},
			}},
			bson.M{mongoOp: bson.A{l.expr, r.expr}},
		}}, nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}
}

// equalityExpr is the equality of l and r; an array field compared with a non-array operand also matches when it holds it.
func equalityExpr(l, r exprOperand) any {
	eq := bson.M{opEq: bson.A{l.expr, r.expr}}

	switch {
	case l.array && !r.array:
		return bson.M{opOr: bson.A{eq, memberExpr(l.expr, r.expr)}}
	case r.array && !l.array:
		return bson.M{opOr: bson.A{eq, memberExpr(r.expr, l.expr)}}
	default:
		return eq
	}
}

// memberExpr tests whether the array list holds value; a list that is not an array holds nothing ($in fails on a non-array).
func memberExpr(list, value any) bson.M {
	return bson.M{opCond: bson.A{bson.M{opIsArray: list}, bson.M{opIn: bson.A{value, list}}, false}}
}

// translateDynamicIn translates `left in [...]` whose list holds a non-literal element, or whose left side is a substring(), as
// the disjunction of the literal members (one regular in) and one equality per other element.
func (t *FilterTranslator) translateDynamicIn(left filter.Node, list *filter.ListNode) (bson.M, error) {
	var (
		literals []filter.Node
		terms    bson.A
	)

	substring := isSubstring(left)

	for _, elem := range list.Elements {
		if _, isLit := elem.(*filter.LiteralNode); isLit && !substring {
			literals = append(literals, elem)
			continue
		}

		term, err := t.translateComparison(filter.OpEqual, left, elem)
		if err != nil {
			return nil, err
		}

		terms = append(terms, term)
	}

	if len(literals) != 0 {
		term, err := t.translateIn(left, &filter.ListNode{Elements: literals})
		if err != nil {
			return nil, err
		}

		terms = append(bson.A{term}, terms...)
	}

	switch len(terms) {
	case 0:
		// An empty list holds no member: never match ($or with no terms is rejected by the server).
		return bson.M{opExpr: false}, nil
	case 1:
		m, _ := terms[0].(bson.M) //nolint:errcheck // every term is a bson.M
		return m, nil
	}

	return bson.M{opOr: terms}, nil
}

// hasDynamicElement reports whether list holds an element that is not a literal.
func hasDynamicElement(list *filter.ListNode) bool {
	for _, elem := range list.Elements {
		if _, isLit := elem.(*filter.LiteralNode); !isLit {
			return true
		}
	}

	return false
}

// translateStringExpr translates contains, startsWith or endsWith with a field argument: both values must be strings.
func (t *FilterTranslator) translateStringExpr(op filter.Operator, target, arg filter.Node) (bson.M, error) {
	argIdent, isIdent := arg.(*filter.IdentNode)
	if !isIdent {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a string function argument", arg)
	}

	if t.isArrayField(target) || t.isArrayField(arg) {
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "string function of an array field with a field argument")
	}

	if err := filter.CheckFieldReference(target); err != nil {
		return nil, err
	}

	ident, ok := target.(*filter.IdentNode)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "string function of a computed value")
	}

	a, err := t.fieldOperand(ident)
	if err != nil {
		return nil, err
	}

	b, err := t.fieldOperand(argIdent)
	if err != nil {
		return nil, err
	}

	var core any

	switch op {
	case filter.OpContains:
		core = bson.M{opNe: bson.A{bson.M{opIndexCP: bson.A{a.expr, b.expr}}, -1}}
	case filter.OpStartsWith:
		// The first occurrence is at 0 exactly when b is a prefix.
		core = bson.M{opEq: bson.A{bson.M{opIndexCP: bson.A{a.expr, b.expr}}, 0}}
	default: // OpEndsWith
		lenA, lenB := bson.M{opStrLenCP: a.expr}, bson.M{opStrLenCP: b.expr}
		core = bson.M{opCond: bson.A{
			bson.M{opGte: bson.A{lenA, lenB}},
			bson.M{opEq: bson.A{bson.M{opSubstrCP: bson.A{a.expr, bson.M{opSubtract: bson.A{lenA, lenB}}, lenB}}, b.expr}},
			false,
		}}
	}

	// $cond evaluates only the branch it takes: $indexOfCP, $strLenCP and $substrCP fail on a non-string.
	return bson.M{opExpr: bson.M{opCond: bson.A{
		bson.M{opAnd: bson.A{
			bson.M{opEq: bson.A{bson.M{opType: a.expr}, typeString}},
			bson.M{opEq: bson.A{bson.M{opType: b.expr}, typeString}},
		}},
		core,
		false,
	}}}, nil
}

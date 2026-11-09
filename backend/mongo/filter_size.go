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

// translateSizeComparison handles size() comparisons like tags.size() == 3.
//
// size() measures an array or a string. Arrays keep the $size forms, so an
// absent or null field behaves as it always has; a string is measured in code
// points with $strLenCP, as CEL defines size(). See stringLength for the
// guard that keeps $strLenCP off non-string values. A field declared by
// [filter.WithArrayFields], and with [WithSchemaSizeZeroWhenAbsent] any field
// not declared by [WithSchemaZeroWhenAbsent], is measured by buildArraySizeFilter.
func (t *FilterTranslator) translateSizeComparison(op filter.Operator, call *filter.CallNode, right filter.Node) (bson.M, error) {
	if call.Target == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
	}

	field, err := t.getFieldName(call.Target)
	if err != nil {
		return nil, err
	}

	value, err := filter.AcceptLiteral(visitor{t}, right)
	if err != nil {
		return nil, err
	}

	if err = CheckSizeOperand(fieldLabel(call.Target), value); err != nil {
		return nil, err
	}

	if (op == filter.OpEqual || op == filter.OpNotEqual) && !isWhole(value) {
		// No size equals a fractional operand and every size differs from it, a value without a size included; MongoDB
		// rejects it as a $size operand. The ordering operators keep their $expr forms, which compare a size with any number.
		return bson.M{opExpr: op == filter.OpNotEqual}, nil
	}

	if ident, isIdent := call.Target.(*filter.IdentNode); isIdent && t.semantics.MeasuresResolvedSize(ident.Name, t.config) {
		return t.buildArraySizeFilter(field, op, value)
	}

	kind, declared := t.zeroKind(call.Target)
	if !declared {
		return t.buildSizeFilter(field, op, value)
	}

	base, err := t.buildSizeFilter(field, op, value)
	if err != nil {
		return nil, err
	}

	return absentAs(field, zeroMatchesSize(op, kind, value), base), nil
}

// isWhole reports whether a valid size() operand ([CheckSizeOperand]) is a whole number.
func isWhole(value any) bool {
	f, isFloat := value.(float64)
	return !isFloat || f == math.Trunc(f)
}

// buildSizeFilter builds the size() comparison of field against value.
func (t *FilterTranslator) buildSizeFilter(field string, op filter.Operator, value any) (bson.M, error) {
	switch op {
	case filter.OpEqual:
		return bson.M{"$or": bson.A{
			bson.M{field: bson.M{opSize: value}},
			bson.M{opExpr: bson.M{opEq: bson.A{stringLength(field), value}}},
		}}, nil
	case filter.OpNotEqual:
		return bson.M{opAnd: bson.A{
			bson.M{field: bson.M{"$not": bson.M{opSize: value}}},
			bson.M{opExpr: bson.M{opNe: bson.A{stringLength(field), value}}},
		}}, nil
	case filter.OpGT, filter.OpGTE, filter.OpLT, filter.OpLTE:
		return t.buildSizeExprFilter(field, op, value)
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "size comparison with operator %v", op)
	}
}

// stringLength is an aggregation expression for the code-point length of
// field when it holds a string, and null otherwise. $cond evaluates only the
// branch it takes, so $strLenCP — which fails the whole query on a non-string —
// never sees anything else.
func stringLength(field string) bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{opEq: bson.A{bson.M{"$type": "$" + field}, "string"}},
		bson.M{"$strLenCP": "$" + field},
		nil,
	}}
}

// sizeLength is an aggregation expression for the size of field: an absent or null value counts as an empty array, an array by its
// element count, a string by its code points; any other value yields null. The length is spelled out twice rather than bound with
// $let: a $let document carries plain keys (vars, in) that read like field references.
func sizeLength(field string) bson.M {
	orEmpty := bson.M{"$ifNull": bson.A{"$" + field, bson.A{}}}

	return bson.M{"$cond": bson.A{
		bson.M{"$isArray": orEmpty},
		bson.M{opSize: orEmpty},
		stringLength(field),
	}}
}

// buildSizeExprFilter builds a $expr filter for size ordering comparisons. An
// absent or null field counts as an empty array (length 0), an array by its
// element count and a string by its code points; any other value has no size
// and never matches.
func (t *FilterTranslator) buildSizeExprFilter(field string, op filter.Operator, value any) (bson.M, error) {
	mongoOp, ok := orderingOps[op]
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "size comparison with operator %v", op)
	}

	length := sizeLength(field)

	return bson.M{opExpr: bson.M{opAnd: bson.A{
		bson.M{opNe: bson.A{length, nil}},
		bson.M{mongoOp: bson.A{length, value}},
	}}}, nil
}

// buildArraySizeFilter builds the size() comparison of a field declared by [filter.WithArrayFields] (or measured so under
// [WithSchemaSizeZeroWhenAbsent]). Every operator measures the
// value the aggregation path "$field" resolves to — an array of documents yields the values found under the rest of the path — with
// an absent or null value counting as an empty array; a string counts its code points and any other value has no size, so only !=
// holds for it.
func (t *FilterTranslator) buildArraySizeFilter(field string, op filter.Operator, value any) (bson.M, error) {
	switch op {
	case filter.OpEqual:
		return bson.M{opExpr: bson.M{opEq: bson.A{sizeLength(field), value}}}, nil
	case filter.OpNotEqual:
		return bson.M{opExpr: bson.M{opNe: bson.A{sizeLength(field), value}}}, nil
	default:
		return t.buildSizeExprFilter(field, op, value)
	}
}

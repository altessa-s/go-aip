// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"fmt"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// isArrayField reports whether node is a field declared by [filter.WithArrayFields].
func (t *FilterTranslator) isArrayField(node filter.Node) bool {
	ident, ok := node.(*filter.IdentNode)
	return ok && t.config.IsArrayField(ident.Name)
}

// errNeedsNullEmpty reports what that needs IS NULL / IS EMPTY on a server older than 1.2.
func errNeedsNullEmpty(what string) error {
	return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%s needs IS NULL / IS EMPTY (Meilisearch 1.2 or later)", what)
}

// nullPredicate renders `field == null`: the attribute is absent from the document, or present and null.
func (t *FilterTranslator) nullPredicate(field string) (string, error) {
	if !t.features.nullEmpty {
		return "", errNeedsNullEmpty("comparison with null")
	}

	return "(" + field + " IS NULL OR " + field + " NOT EXISTS)", nil
}

// emptyStringPredicate renders `field == ""`. IS EMPTY also matches `[]` and `{}`, which a string field cannot hold;
// on an array field it would test the array rather than an element, so there it is refused.
func (t *FilterTranslator) emptyStringPredicate(field string, array bool) (string, error) {
	if array {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "empty-string element of array field %s", field)
	}

	if !t.features.nullEmpty {
		return "", errNeedsNullEmpty("comparison with the empty string")
	}

	return field + " IS EMPTY", nil
}

// isEmptyString reports whether v is the empty string (or empty bytes, rendered as a string).
func isEmptyString(v any) bool {
	switch val := v.(type) {
	case string:
		return val == ""
	case []byte:
		return len(val) == 0
	default:
		return false
	}
}

// translateSizeComparison handles a size() comparison. Meilisearch filters cannot count, but they can test emptiness, so
// the comparisons that only separate size 0 from the rest (`== 0`, `!= 0`, `> 0`, `>= 1`, `< 1`, `<= 0`, either operand
// order) render with IS EMPTY; any other size comparison fails with [filter.ErrUnsupportedOperation]. negated renders the
// negation of the comparison: the opposite emptiness test, which keeps a null or absent non-array value unmatched, as the
// evaluator keeps size() of null unknown under !.
func (t *FilterTranslator) translateSizeComparison(op filter.Operator, left, right filter.Node, negated bool) (string, error) {
	call, operand := left, right
	if !isSizeCall(left) {
		call, operand, op = right, left, mirror(op)
	}

	sizeCall, ok := call.(*filter.CallNode)
	if !ok || isSizeCall(operand) {
		return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "size() compared with size()")
	}

	target := sizeCall.Target

	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	value, err := filter.AcceptLiteral(visitor{t}, operand)
	if err != nil {
		return "", err
	}

	n, ok := sizeOperand(value)
	if !ok {
		return "", &coreerrs.FieldError{Field: field, Err: filter.ErrInvalidExpression,
			Reason: fmt.Sprintf("size() is compared with a number, not this %T", value)}
	}

	empty, ok := emptiness(op, n)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation,
			"size() %v %v: only emptiness tests (== 0, != 0, > 0, >= 1, < 1, <= 0) have a Meilisearch counterpart", op, value)
	}

	if !t.features.nullEmpty {
		return "", errNeedsNullEmpty("size() emptiness test")
	}

	if negated {
		empty = !empty
	}

	if !empty {
		// IS NOT EMPTY is NOT (IS EMPTY), which also holds for an absent or null attribute: a value without a size.
		return "(" + field + " EXISTS AND " + field + " IS NOT NULL AND " + field + " IS NOT EMPTY)", nil
	}

	if t.isArrayField(target) {
		// An absent or null array is empty, as the evaluator and the MongoDB translator read it.
		return "(" + field + " NOT EXISTS OR " + field + " IS NULL OR " + field + " IS EMPTY)", nil
	}

	return field + " IS EMPTY", nil
}

// sizeOperand converts a numeric size() operand to float64.
func sizeOperand(v any) (float64, bool) {
	switch val := v.(type) {
	case int64:
		return float64(val), true
	case uint64:
		return float64(val), true
	case float64:
		return val, true
	default:
		return 0, false
	}
}

// emptiness reports whether `size op n` holds for size 0 alone (empty=true) or for every size above 0 (empty=false);
// ok is false for a comparison with any other set of sizes.
func emptiness(op filter.Operator, n float64) (empty, ok bool) {
	switch op {
	case filter.OpEqual:
		return true, n == 0
	case filter.OpNotEqual:
		return false, n == 0
	case filter.OpLT:
		return true, n > 0 && n <= 1
	case filter.OpLTE:
		return true, n >= 0 && n < 1
	case filter.OpGT:
		return false, n >= 0 && n < 1
	case filter.OpGTE:
		return false, n > 0 && n <= 1
	default:
		return false, false
	}
}

// mirror returns the operator of the comparison with its operands swapped.
func mirror(op filter.Operator) filter.Operator {
	switch op {
	case filter.OpLT:
		return filter.OpGT
	case filter.OpLTE:
		return filter.OpGTE
	case filter.OpGT:
		return filter.OpLT
	case filter.OpGTE:
		return filter.OpLTE
	default:
		return op
	}
}

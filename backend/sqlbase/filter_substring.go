// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strconv"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// maxSubstringArgs is the number of bounds of substring(start, end).
const maxSubstringArgs = 2

// SubstringBounds reads the bounds of `field.substring(start[, end])`: one or two int literals with 0 <= start <= end. It
// returns the zero-based start, the length in code points (-1 for a substring that runs to the end) and the guard: the
// length in code points a string needs for the substring to exist, as the evaluator finds the substring of a shorter
// string unknown (0 needs no guard). A bound that is not a literal fails with [filter.ErrUnsupportedOperation]; a wrong
// argument count, a literal that is not an int and bounds out of range or out of order fail with
// [filter.ErrInvalidExpression]. The schema translators share it with the walker.
//
// Any int64 bound is accepted, as the evaluator accepts it: a bound past every stored text only makes the substring
// unknown through the guard. The order is checked before end - start is computed, so the length never overflows. A
// [SubstringDialect] must render the one-based start without overflow and within the range its server's function takes
// (see [SubstringStart]).
func SubstringBounds(args []filter.Node) (start, length, guard int64, err error) {
	if len(args) < 1 || len(args) > maxSubstringArgs {
		return 0, 0, 0, coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	bounds := make([]int64, 0, maxSubstringArgs)

	for _, arg := range args {
		lit, isLit := arg.(*filter.LiteralNode)
		if !isLit || lit == nil {
			return 0, 0, 0, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "substring() bound %T", arg)
		}

		v, isInt := lit.Value.(int64)
		if !isInt {
			return 0, 0, 0, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bound must be int, got %T", lit.Value)
		}

		bounds = append(bounds, v)
	}

	// Both bounds are checked before the subtraction: with 0 <= start <= end, end - start cannot overflow.
	if bounds[0] < 0 || (len(bounds) == maxSubstringArgs && bounds[1] < bounds[0]) {
		return 0, 0, 0, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bounds %v out of range", bounds)
	}

	start, length, guard = bounds[0], -1, bounds[0]
	if len(bounds) == maxSubstringArgs {
		length, guard = bounds[1]-start, bounds[1]
	}

	return start, length, guard, nil
}

// SubstringStart returns the one-based position of the zero-based start, capped at limit (the largest position the
// server's substring function takes, math.MaxInt64 when it takes any int64). Only a start whose guard — a text of more
// than limit code points — can never hold reaches the cap, so the capped value is never compared.
func SubstringStart(start, limit int64) int64 {
	if start >= limit {
		return limit
	}

	return start + 1
}

// substringSQL renders `field.substring(start[, end])` through the dialect and returns the row guard
// `size(field) >= bound` that keeps an out-of-range bound from matching a truncated string: the evaluator finds such
// a row unknown, SQL would silently compare a shorter value. The guard is empty when the bound is zero.
func (t *FilterTranslator) substringSQL(n *filter.CallNode) (expr, guard string, err error) {
	subs, ok := t.dialect.(SubstringDialect)
	if !ok {
		return "", "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() is not supported by this dialect")
	}

	if len(n.Args) < 1 || len(n.Args) > maxSubstringArgs {
		return "", "", coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	if t.isArrayField(n.Target) {
		return "", "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() of an array field")
	}

	col, err := t.column(n.Target)
	if err != nil {
		return "", "", err
	}

	start, length, bound, err := SubstringBounds(n.Args)
	if err != nil {
		return "", "", err
	}

	if bound > 0 {
		guard = t.dialect.SizeExpr(col) + " >= " + strconv.FormatInt(bound, 10)
	}

	return subs.SubstringExpr(col, start, length), guard, nil
}

// substringGuards collects the row guards of the substring() operands among nodes.
func (t *FilterTranslator) substringGuards(nodes ...filter.Node) (string, error) {
	var guard string

	for _, node := range nodes {
		call, ok := node.(*filter.CallNode)
		if !ok || call.Op != filter.OpSubstring {
			continue
		}

		_, g, err := t.substringSQL(call)
		if err != nil {
			return "", err
		}

		if g == "" {
			continue
		}

		if guard != "" {
			guard += " AND "
		}

		guard += g
	}

	return guard, nil
}

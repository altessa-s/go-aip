// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"math"
	"strconv"

	"github.com/shopspring/decimal"

	filter "github.com/altessa-s/go-aip/filter"
)

// exactLiteral returns the operand v produced by node for a comparison against other: when other is a decimal.Decimal and node is
// a float literal with a recorded source spelling, the spelling (a decimal string read exactly by [filter.DecimalLiteral]); otherwise v.
func exactLiteral(other any, node filter.Node, v any) any {
	if _, ok := other.(decimal.Decimal); !ok {
		return v
	}

	if lit, ok := node.(*filter.LiteralNode); ok && lit.Text != "" {
		return lit.Text
	}

	return v
}

// compareDecimal compares two operands at least one of which is a decimal.Decimal, exactly. ok is false when the other operand
// cannot be read as a decimal ([filter.DecimalLiteral]).
func compareDecimal(left, right any) (c int, ok bool, err error) {
	_, lok := left.(decimal.Decimal)

	_, rok := right.(decimal.Decimal)
	if !lok && !rok {
		return 0, false, nil
	}

	l, err := filter.DecimalLiteral(left)
	if err != nil {
		return 0, true, err
	}

	r, err := filter.DecimalLiteral(right)
	if err != nil {
		return 0, true, err
	}

	return l.Cmp(r), true, nil
}

// The default evaluator (without WithProfile) compares a field declared [filter.FieldKindDecimal] exactly, whatever Go form its
// data takes, as the SQL and MongoDB translators compare a decimal column: the data value is read as a decimal ([evaluation.decimalData])
// and the operand as the exact decimal the expression spells ([evaluation.decimalOperand]), so `price == "1.5"` and `price > 1`
// hold on a float64 1.5, a string "1.50" and a decimal.Decimal alike. A profile applies its own rule (the MongoDB profile keeps the
// stored form, as MongoDB compares a stored string or double with a Decimal128 operand).

// isDecimalField reports whether n names a field declared [filter.FieldKindDecimal].
func (e *evaluation) isDecimalField(n filter.Node) bool {
	ident, ok := n.(*filter.IdentNode)
	return ok && ident != nil && e.config.FieldKind(ident.Name) == filter.FieldKindDecimal
}

// decimalData returns v, a data value of a [filter.FieldKindDecimal] field, as an exact decimal: a decimal.Decimal as it is, an
// integer by its value, a finite float64 as the shortest decimal that round-trips to it, a string through the policy's decimal parser
// ([filter.WithDecimalParser]). Any other value — null, a NaN, a string that is no decimal — is returned unchanged and compares as
// the value it is. Parsing a string is charged to the data budget.
func (e *evaluation) decimalData(v any) any {
	var spelled any

	switch x := v.(type) {
	case decimal.Decimal:
		return v
	case int64, int32, int, uint64:
		spelled = x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return v
		}

		spelled = strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		if !e.budget.chargeScan(len(x)) {
			return v
		}

		spelled = x
	default:
		return v
	}

	d, err := e.config.DecimalOperand(nil, spelled)
	if err != nil {
		return v
	}

	return d
}

// decimalOperand returns the operand v, produced by node, of a comparison with a [filter.FieldKindDecimal] field as an exact
// decimal: a literal as it is spelled ([filter.Policy.DecimalOperand]), a data value by [evaluation.decimalData]. Null stays null.
func (e *evaluation) decimalOperand(node filter.Node, v any) (any, error) {
	if v == nil {
		return nil, nil //nolint:nilnil // null is a valid operand
	}

	if _, isLiteral := node.(*filter.LiteralNode); !isLiteral {
		return e.decimalData(v), nil
	}

	d, err := e.config.DecimalOperand(node, v)
	if err != nil {
		return nil, err
	}

	return d, nil
}

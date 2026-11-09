// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"math"
	"math/big"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/decimal128"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Decimal literal bounds.
const (
	// MaxExactFloatDigits is the most significant digits a float literal may have to be read as an exact decimal: every decimal with
	// at most 15 significant digits survives the round trip through a float64, so the shortest representation of the parsed float
	// is the decimal that was written.
	MaxExactFloatDigits = 15

	// MaxDecimalStringLength bounds a string [DecimalLiteral] reads as a decimal before it is parsed, in time quadratic in its
	// length: it holds every Decimal128 written with its leading zeros (0.000…1 down to the smallest exponent) and the trailing
	// zeros [decimal128.MaxCoefficientBits] allows. A longer string — a data value compared with a decimal, say — is not a decimal.
	MaxDecimalStringLength = 8192
)

// DecimalLiteral returns v as an exact decimal, the way [FieldKindDecimal] reads an operand:
//
//   - a decimal.Decimal;
//   - an integer (int64, uint64, int32, int) exactly;
//   - a float64 as the shortest decimal that round-trips to it ("0.1" for the float parsed from 0.1), provided it has at most
//     [MaxExactFloatDigits] significant digits — a longer one cannot be told apart from its neighbors, so write it as a string;
//     NaN and infinities are rejected. A float parsed from a filter expression is read from its source spelling instead
//     ([DecimalOperand]);
//   - a string in decimal notation ("12.30", "-1e-2"), within the default coefficient budget.
//
// Results are normalized (zero as 0, trailing zeros folded into the exponent). Input length, coefficient size and exponents
// are bounded to limit comparison work independently of database representation. An
// extreme exponent such as "0e-999999999" can never reach a comparison. Anything else fails with [ErrFieldTypeMismatch].
func DecimalLiteral(v any) (decimal.Decimal, error) {
	switch n := v.(type) {
	case decimal.Decimal:
		return boundedDecimal(n)
	case int64:
		return decimal.NewFromInt(n), nil
	case int32:
		return decimal.NewFromInt32(n), nil
	case int:
		return decimal.NewFromInt(int64(n)), nil
	case uint64:
		return decimal.NewFromUint64(n), nil
	case float64:
		return floatDecimal(n)
	case string:
		return stringDecimal(n)
	default:
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "%s is not a decimal", valueKindName(v))
	}
}

// DecimalOperand is [DecimalLiteral] for an operand produced by node: a float literal parsed from an expression is read from its
// exact source spelling (`1.00000000000000001` stays 1.00000000000000001 although the float it parsed to is 1), bounded like a
// decimal string; any other node uses v. Translators of [FieldKindDecimal] fields call it with the literal node and its value.
func DecimalOperand(node Node, v any) (decimal.Decimal, error) {
	if lit, ok := node.(*LiteralNode); ok && lit.Text != "" {
		return stringDecimal(lit.Text)
	}

	return DecimalLiteral(v)
}

// stringDecimal parses a decimal string within the default resource bounds.
func stringDecimal(s string) (decimal.Decimal, error) {
	if len(s) > MaxDecimalStringLength {
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "a %d-byte string is not a decimal number", len(s))
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "%q is not a decimal number", s)
	}

	return boundedDecimal(d)
}

// floatDecimal reads f as the shortest decimal that round-trips to it.
func floatDecimal(f float64) (decimal.Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "%v is not a finite decimal", f)
	}

	d, err := decimal.NewFromString(strconv.FormatFloat(f, 'g', -1, 64))
	if err != nil {
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "%v: %w", f, err)
	}

	if digits, _, _ := normalizeDecimal(d); digits > MaxExactFloatDigits {
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch,
			"float %v has %d significant digits, more than the %d that are exact; write it as a decimal string", f, digits, MaxExactFloatDigits)
	}

	return boundedDecimal(d)
}

// decimalBase is the radix of a decimal exponent.
const decimalBase = 10

// boundedDecimal normalizes a value within the default coefficient and exponent work budgets.
func boundedDecimal(d decimal.Decimal) (decimal.Decimal, error) {
	digits, exp, ok := normalizeDecimal(d)
	switch {
	case !ok:
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch, "decimal coefficient exceeds %d bits", decimal128.MaxCoefficientBits)
	case digits == 0:
		return decimal.New(0, 0), nil
	case exp < -16383 || exp > 16383:
		return decimal.Decimal{}, coreerrs.Wrapf(ErrFieldTypeMismatch,
			"decimal with %d significant digits and exponent %d exceeds the default decimal exponent budget", digits, exp)
	}

	c := d.Coefficient()
	if shift := exp - int64(d.Exponent()); shift > 0 {
		c.Quo(c, new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(shift), nil)) // shift <= the coefficient's digit count
	}

	return decimal.NewFromBigInt(c, int32(exp)), nil //nolint:gosec // exp is within the bounded int32 range
}

// normalizeDecimal returns the number of digits of d's coefficient without trailing zeros and the exponent that goes with that
// coefficient (0 digits for zero, whatever its exponent). ok is false for a coefficient above [decimal128.MaxCoefficientBits].
func normalizeDecimal(d decimal.Decimal) (digits int, exp int64, ok bool) {
	if d.Sign() == 0 {
		return 0, 0, true
	}

	if !decimal128.CoefficientWithinLimit(d) { // before Coefficient copies it
		return 0, 0, false
	}

	digits, exp = decimal128.Normalize(d)

	return digits, exp, true
}

// DecimalOperand converts an operand using this policy's decimal parser.
func (o *Policy) DecimalOperand(node Node, value any) (decimal.Decimal, error) {
	return o.decimalParser(node, value)
}

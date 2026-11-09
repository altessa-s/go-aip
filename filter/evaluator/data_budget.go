// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/decimal128"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// The AST limits bound the nodes of a filter, not the data it reads: a path through a map holding a list of itself doubles at
// every step, a typed slice is copied to be read, a string function scans its whole string. A dataBudget charges that work to one
// [Evaluator.Evaluate] call ([WithMaxDataBudget]); a charge beyond it exhausts the budget, every later charge fails, the work in
// progress stops, and the evaluation fails with [filter.ErrDataLimitExceeded] whatever it found so far.

// Costs, in [Budget] units, of the work that is not one unit per value, which the evaluator and a [ValueEncoding] share.
const (
	// BytesPerUnit is the bytes of a data string or binary one unit scans.
	BytesPerUnit = 64
	// EncodedBytesPerUnit is the bytes of a value one unit encodes or decodes through an encoding.
	EncodedBytesPerUnit = 16
	// DecimalCompareCost is a comparison involving a decimal, which normalizes it ([filter.DecimalLiteral]).
	DecimalCompareCost = 64
)

// Costs, in budget units, of the evaluator's own work that is not one unit per value.
const (
	// decimalExponentsPerUnit adds a unit to [DecimalCompareCost] per exponent steps the comparison rescales or the data string it
	// parses holds.
	decimalExponentsPerUnit = 8
	// regexBytesPerUnit is the bytes of the pattern one more unit per scanned chunk accounts for: RE2 matches in time linear in
	// the input, by a factor that grows with the pattern.
	regexBytesPerUnit = 16
)

// dataBudget is the work an evaluation may still spend reading its data, and the [ValueEncoding] it reads stored forms through
// (nil without one).
type dataBudget struct {
	left      int
	exhausted bool
	enc       ValueEncoding
	ext       Budget // when set, the budget charged instead of left ([StoredValue])
}

// Charge is charge, the [Budget] a [ValueEncoding] spends.
func (b *dataBudget) Charge(units int) bool {
	return b.charge(units)
}

// charge spends units, or exhausts the budget and reports false when fewer are left.
func (b *dataBudget) charge(units int) bool {
	if b.ext != nil {
		if b.exhausted || units < 0 || !b.ext.Charge(units) {
			return b.exhaust()
		}

		return true
	}

	if units > b.left || units < 0 {
		return b.exhaust()
	}

	b.left -= units

	return true
}

// exhaust spends what is left of the budget and fails every later charge; it reports false.
func (b *dataBudget) exhaust() bool {
	b.left, b.exhausted = 0, true
	return false
}

// chargeScan spends the scan of a string or binary of n bytes.
func (b *dataBudget) chargeScan(n int) bool {
	return b.charge(1 + n/BytesPerUnit)
}

// chargeRegex spends matching a pattern of patternLen bytes against a string of n bytes.
func (b *dataBudget) chargeRegex(n, patternLen int) bool {
	chunks, factor := 1+n/BytesPerUnit, 1+patternLen/regexBytesPerUnit
	if chunks > b.left/factor { // checked before multiplying, which could overflow
		return b.exhaust()
	}

	return b.charge(chunks * factor)
}

// chargeComparison spends comparing the data value a with c ([comparisonCost]); a comparison of two values of the common kinds,
// neither a string, bytes nor a decimal, costs a unit without looking further.
func (b *dataBudget) chargeComparison(a, c any) bool {
	if x, ok := a.(string); ok {
		if y, ok := c.(string); ok { // comparisonCost of two strings, without its type switches
			return b.charge(1 + min(len(x), len(y))/BytesPerUnit)
		}
	}

	units := 1
	if costlyOperand(b.enc, a) || costlyOperand(b.enc, c) {
		units = comparisonCost(b.enc, a, c)
	}

	return b.charge(units)
}

// costlyOperand reports whether comparing v may cost more than a unit: it is a decimal, or holds bytes compared one by one
// ([scanLength]).
func costlyOperand(enc ValueEncoding, v any) bool {
	switch v.(type) {
	case string, []byte, decimal.Decimal:
		return true
	case nil, bool, int64, int32, int, uint64, float64, time.Time:
		return false
	default:
		_, ok := scanLength(enc, v)
		return ok
	}
}

// scanLength returns the bytes comparing v with a value of its class may scan: those of a string or a binary, and those the
// encoding reports for its own values (the strings of a symbol, JavaScript code, a regular expression or a DB pointer, the data
// of a binary). ok is false for any other value.
func scanLength(enc ValueEncoding, v any) (n int, ok bool) {
	switch x := v.(type) {
	case string:
		return len(x), true
	case []byte:
		return len(x), true
	case nil, bool, int64, int32, int, uint64, float64, time.Time, decimal.Decimal:
		return 0, false
	default:
		if enc == nil {
			return 0, false
		}

		if ext, ok := enc.(ScanSizer); ok {
			return ext.ScanLength(v)
		}

		return 0, false
	}
}

// dataLimitError is [filter.ErrDataLimitExceeded] for a budget of limit units.
func dataLimitError(limit int) error {
	return coreerrs.Wrapf(filter.ErrDataLimitExceeded, "reading the data needs more than %d units", limit)
}

// comparisonCost is the cost of comparing a with b: one unit; for two values that hold bytes compared one by one ([scanLength]) a
// unit per [BytesPerUnit] bytes of the shorter, which bounds a comparison of two data values (a literal operand bounds it already);
// for a comparison involving a decimal the normalization of the decimal and a unit per [decimalExponentsPerUnit] steps of
// rescaling or of a string parsed as a decimal.
func comparisonCost(enc ValueEncoding, a, b any) int {
	if x, ok := scanLength(enc, a); ok {
		if y, ok := scanLength(enc, b); ok {
			return 1 + min(x, y)/BytesPerUnit
		}
	}

	_, aDecimal := a.(decimal.Decimal)

	_, bDecimal := b.(decimal.Decimal)
	if !aDecimal && !bDecimal {
		return 1
	}

	return DecimalCompareCost + (decimalWork(a)+decimalWork(b))/decimalExponentsPerUnit
}

// decimalWork is the size of v as a decimal operand: the magnitude of a decimal's exponent — up to the Decimal128 range, beyond
// which [filter.DecimalLiteral] refuses it before rescaling — and the length of a string parsed as one, up to the longest it parses.
func decimalWork(v any) int {
	switch x := v.(type) {
	case decimal.Decimal:
		e := int(x.Exponent())
		return min(max(e, -e), -decimal128.MinExp)
	case string:
		return min(len(x), filter.MaxDecimalStringLength)
	default:
		return 0
	}
}

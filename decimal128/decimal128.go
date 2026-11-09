// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package decimal128

import (
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

// Decimal128 bounds.
const (
	// MaxDigits is the most significant digits of a Decimal128 coefficient.
	MaxDigits = 34
	// MinExp is the smallest Decimal128 exponent.
	MinExp = -6176
	// MaxExp is the largest Decimal128 exponent.
	MaxExp = 6111
)

// decimalBase is the radix of a decimal exponent.
const decimalBase = 10

// Normalize returns the number of significant digits of d's coefficient once
// its trailing zeros are folded into the exponent, and that exponent. A zero
// is (0, 0), whatever its exponent. It formats the coefficient, in time
// superlinear in its length, so a caller holding untrusted input bounds the
// coefficient's size first (see [MaxCoefficientBits]); [Fits] and [Canonical]
// do.
func Normalize(d decimal.Decimal) (digits int, exp int64) {
	if d.Sign() == 0 {
		return 0, 0
	}

	digits, trailing := coefficientDigits(d)

	return digits - trailing, int64(d.Exponent()) + int64(trailing)
}

// Representable reports whether a decimal normalized by [Normalize] to digits
// significant digits and exponent exp is held exactly by a Decimal128: at most
// [MaxDigits] digits, an exponent of at least [MinExp], and an exponent that
// padding the coefficient up to [MaxDigits] digits brings down to [MaxExp]. A
// zero (0 digits) is always representable.
func Representable(digits int, exp int64) bool {
	if digits == 0 {
		return true
	}

	return digits <= MaxDigits && exp >= MinExp && exp <= int64(MaxExp+MaxDigits-digits)
}

// Fits reports whether a Decimal128 holds d exactly, without rounding: whether
// its coefficient has at most [MaxCoefficientBits] bits and it is
// [Representable] once [Normalize]d. It judges the stored value, not the
// decimal the caller keeps: a zero fits with any exponent, and a fitting
// coefficient may carry trailing zeros; [Canonical] returns the bounded form.
func Fits(d decimal.Decimal) bool {
	return CoefficientWithinLimit(d) && Representable(Normalize(d))
}

// Canonical returns d as a Decimal128 stores it and reads it back: the same
// value with at most [MaxDigits] digits and an exponent in [[MinExp],
// [MaxExp]], changed only as far as needed — trailing zeros are dropped to
// shorten a coefficient beyond 34 digits or to raise an exponent below the
// range, and zeros are appended to lower an exponent above it. A zero is
// decimal.Zero. ok is false when d does not [Fits].
func Canonical(d decimal.Decimal) (canonical decimal.Decimal, ok bool) {
	if d.Sign() == 0 {
		return decimal.Zero, true
	}

	if !CoefficientWithinLimit(d) { // before Coefficient copies it
		return decimal.Decimal{}, false
	}

	n, trailing := coefficientDigits(d)

	exp := int64(d.Exponent())
	if !Representable(n-trailing, exp+int64(trailing)) {
		return decimal.Decimal{}, false
	}

	target := exp
	if n > MaxDigits {
		target += int64(n - MaxDigits)
	}

	target = min(max(target, MinExp), MaxExp)
	c := d.Coefficient()

	switch shift := target - exp; {
	case shift > 0: // shift <= trailing: Representable holds
		c.Quo(c, pow10(shift))
	case shift < 0: // -shift <= MaxDigits - n
		c.Mul(c, pow10(-shift))
	}

	return decimal.NewFromBigInt(c, int32(target)), true //nolint:gosec // target is within the Decimal128 range
}

// coefficientDigits returns the number of digits of d's coefficient and how
// many of them are trailing zeros.
func coefficientDigits(d decimal.Decimal) (digits, trailing int) {
	s := strings.TrimPrefix(d.Coefficient().String(), "-")
	return len(s), len(s) - len(strings.TrimRight(s, "0"))
}

// pow10 returns 10^n.
func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(n), nil)
}

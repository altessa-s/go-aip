// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package decimal128

import (
	"math/big"

	"github.com/shopspring/decimal"
)

// Coefficient bounds.
const (
	// MaxCoefficientBits caps the coefficient of a decimal that is inspected:
	// [Fits] and [Canonical] report a decimal.Decimal whose coefficient has
	// more bits as not fitting without copying or formatting it, in time
	// independent of its size, even when folding its trailing zeros would
	// bring it into range ([CoefficientWithinLimit]). 34 digits need 113 bits;
	// the rest is room for trailing zeros.
	MaxCoefficientBits = 4096
	// MaxCoefficientDigits is the most decimal digits a coefficient of
	// [MaxCoefficientBits] bits has.
	MaxCoefficientDigits = 1234
)

// coefficientBits is the bits of the largest coefficient a Decimal128 holds,
// 34 nines.
const coefficientBits = 113

// Bounds on the coefficient of a decimal: coefficientLimit is the largest one
// inspected, coefficientRange one a Decimal128 holds without folding trailing
// zeros into the exponent.
var (
	coefficientLimit = newCoefficientBound(MaxCoefficientBits)
	coefficientRange = newCoefficientBound(coefficientBits)
)

// CoefficientWithinLimit reports whether the coefficient of d has at most
// [MaxCoefficientBits] bits, in time and memory independent of its size: a
// data value may hold any decimal.Decimal, a coefficient of millions of digits
// included, which a caller refuses with it before copying, formatting or
// encoding the coefficient. It checks [SmallCoefficient] first: almost every
// decimal is within it, and its bound is cheaper to compare with.
func CoefficientWithinLimit(d decimal.Decimal) bool {
	return coefficientRange.holds(d) || coefficientLimit.holds(d)
}

// SmallCoefficient reports whether the coefficient of d is below 2^113, the
// bit width of the largest coefficient a Decimal128 holds (34 nines), in time
// and memory independent of its size. It is a bit-width check for pricing the
// work on a decimal, not a representability check: a coefficient below 2^113
// may still have 35 digits (10^34+1) or need its trailing zeros folded
// (10^34), and the exponent does not count. Use [Fits] to tell whether d is
// stored exactly.
func SmallCoefficient(d decimal.Decimal) bool {
	return coefficientRange.holds(d)
}

// coefficientBound is ±2^bits, the bound of a coefficient of at most bits
// bits.
type coefficientBound struct {
	positive, negative *big.Int
}

// newCoefficientBound returns the bound of a coefficient of at most bits bits.
func newCoefficientBound(bits uint) coefficientBound {
	positive := new(big.Int).Lsh(big.NewInt(1), bits)
	return coefficientBound{positive: positive, negative: new(big.Int).Neg(positive)}
}

// holds reports whether the coefficient of d is within b, in time and memory
// independent of its size: Coefficient copies the coefficient, and
// big.Int.Cmp of d with the bound at d's own exponent, which rescales neither,
// compares lengths before words.
func (b coefficientBound) holds(d decimal.Decimal) bool {
	switch d.Sign() {
	case 0:
		return true
	case 1:
		return d.Cmp(decimal.NewFromBigInt(b.positive, d.Exponent())) < 0
	default:
		return d.Cmp(decimal.NewFromBigInt(b.negative, d.Exponent())) > 0
	}
}

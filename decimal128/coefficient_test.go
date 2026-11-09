// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package decimal128_test

import (
	"math/big"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/decimal128"
)

func TestSmallCoefficient(t *testing.T) {
	t.Parallel()

	nines := decimal.RequireFromString(strings.Repeat("9", decimal128.MaxDigits))
	require.True(t, decimal128.SmallCoefficient(nines))
	require.True(t, decimal128.SmallCoefficient(nines.Neg()))
	require.True(t, decimal128.SmallCoefficient(nines.Shift(-40)), "the exponent does not count")
	require.True(t, decimal128.SmallCoefficient(decimal.Zero))
	require.False(t, decimal128.SmallCoefficient(decimal.NewFromBigInt(new(big.Int).Lsh(big.NewInt(1), 113), 0)))

	// A bit-width check, not representability: 10^34+1 has 35 digits and 10^34 must fold a zero.
	ten34 := decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(34), nil), 0)
	tooManyDigits := ten34.Add(decimal.NewFromInt(1))
	require.True(t, decimal128.SmallCoefficient(tooManyDigits))
	require.False(t, decimal128.Fits(tooManyDigits))
	require.True(t, decimal128.SmallCoefficient(ten34))
	require.True(t, decimal128.Fits(ten34), "folding its trailing zero brings 10^34 into range")
}

func TestCoefficientWithinLimit(t *testing.T) {
	t.Parallel()

	require.True(t, decimal128.CoefficientWithinLimit(decimal.Zero))
	require.True(t, decimal128.CoefficientWithinLimit(decimal.NewFromBigInt(new(big.Int).Lsh(big.NewInt(1), 113), 0)),
		"beyond a Decimal128 coefficient, within the limit")

	within := decimal.NewFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), decimal128.MaxCoefficientBits), big.NewInt(1)), 7)
	require.True(t, decimal128.CoefficientWithinLimit(within))
	require.True(t, decimal128.CoefficientWithinLimit(within.Neg()))

	huge := decimal.NewFromBigInt(new(big.Int).Lsh(big.NewInt(1), decimal128.MaxCoefficientBits), -3)
	require.False(t, decimal128.CoefficientWithinLimit(huge), "2^4096 is beyond the limit at any exponent")
	require.False(t, decimal128.CoefficientWithinLimit(huge.Neg()))

	digits := decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(decimal128.MaxCoefficientDigits-1), nil), 0)
	require.True(t, decimal128.CoefficientWithinLimit(digits), "MaxCoefficientDigits digits are within the limit")
	require.False(t, decimal128.CoefficientWithinLimit(digits.Mul(decimal.New(10, 0))), "one more digit is not")
}

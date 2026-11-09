// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package decimal128_test

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/decimal128"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		d      decimal.Decimal
		digits int
		exp    int64
	}{
		{name: "zero", d: decimal.Zero},
		{name: "zero with exponent", d: decimal.New(0, 7000)},
		{name: "plain", d: decimal.RequireFromString("-123.4500"), digits: 5, exp: -2},
		{name: "trailing zeros", d: decimal.New(1200, 3), digits: 2, exp: 5},
		{name: "extreme exponent", d: decimal.New(10, 2147483647), digits: 1, exp: 2147483648},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			digits, exp := decimal128.Normalize(tc.d)
			require.Equal(t, tc.digits, digits)
			require.Equal(t, tc.exp, exp)
		})
	}
}

func TestFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    decimal.Decimal
		want bool
	}{
		{name: "zero", d: decimal.Zero, want: true},
		{name: "zero with extreme exponent", d: decimal.New(0, 2147483647), want: true},
		{name: "34 digits", d: decimal.RequireFromString(strings.Repeat("9", 34)), want: true},
		{name: "35 digits", d: decimal.RequireFromString(strings.Repeat("9", 35)), want: false},
		{name: "35 digits with trailing zero", d: decimal.RequireFromString(strings.Repeat("9", 34) + "0"), want: true},
		{name: "min exponent", d: decimal.New(1, -6176), want: true},
		{name: "below min exponent", d: decimal.New(1, -6177), want: false},
		{name: "below min exponent folds", d: decimal.New(10, -6177), want: true},
		{name: "max exponent", d: decimal.New(1, 6111), want: true},
		{name: "above max exponent pads", d: decimal.New(1, 6144), want: true},
		{name: "above max exponent too many digits", d: decimal.New(1, 6145), want: false},
		{name: "extreme negative exponent", d: decimal.New(1, -2147483648), want: false},
		{name: "extreme positive exponent", d: decimal.New(1, 2147483647), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, decimal128.Fits(tc.d))
			_, ok := decimal128.Canonical(tc.d)
			require.Equal(t, tc.want, ok)
		})
	}
}

// TestCanonical pins the exact stored form; backend/mongo cross-checks it against the driver's own conversion.
func TestCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		d           decimal.Decimal
		coefficient string
		exp         int32
	}{
		{name: "zero is decimal.Zero", d: decimal.New(0, -7000), coefficient: "0", exp: decimal.Zero.Exponent()},
		{name: "trailing zeros kept", d: decimal.RequireFromString("-123.4500"), coefficient: "-1234500", exp: -4},
		{name: "37 digits shortened", d: decimal.RequireFromString(strings.Repeat("9", 34) + "000"), coefficient: strings.Repeat("9", 34), exp: 3},
		{name: "exponent raised to min", d: decimal.New(1000, -6178), coefficient: "10", exp: -6176},
		{name: "exponent lowered to max", d: decimal.New(-12, 6113), coefficient: "-1200", exp: 6111},
		{name: "max padding", d: decimal.New(1, 6144), coefficient: "1" + strings.Repeat("0", 33), exp: 6111},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := decimal128.Canonical(tc.d)
			require.True(t, ok)
			require.Equal(t, tc.coefficient, got.Coefficient().String())
			require.Equal(t, tc.exp, got.Exponent())
			require.Zero(t, got.Cmp(tc.d))
		})
	}
}

// oneWithTrailingZeros returns 1 written as 10^digits × 10^-digits: it is in range once its trailing zeros fold, but its
// coefficient has digits+1 digits.
func oneWithTrailingZeros(digits int64) decimal.Decimal {
	return decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(digits), nil), -int32(digits))
}

// TestOversizedCoefficient checks Fits and Canonical refuse a coefficient beyond MaxCoefficientBits before folding its trailing
// zeros, fast, and still fold one within the bound.
func TestOversizedCoefficient(t *testing.T) {
	t.Parallel()

	const failFast = time.Second // formatting the coefficient and folding its zeros digit by digit takes far longer

	for name, d := range map[string]decimal.Decimal{
		"positive": oneWithTrailingZeros(200_000),
		"negative": oneWithTrailingZeros(200_000).Neg(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()

			require.False(t, decimal128.Fits(d))
			_, ok := decimal128.Canonical(d)
			require.False(t, ok)
			require.Less(t, time.Since(start), failFast)
		})
	}

	// 10^1233 (1234 digits) has MaxCoefficientBits bits; 10^1234 has more.
	within := oneWithTrailingZeros(1233)
	require.LessOrEqual(t, within.Coefficient().BitLen(), decimal128.MaxCoefficientBits)
	require.True(t, decimal128.Fits(within), "a coefficient within the bound still folds")

	got, ok := decimal128.Canonical(within)
	require.True(t, ok)
	require.Equal(t, "1"+strings.Repeat("0", 33), got.Coefficient().String()) // shortened to 34 digits only
	require.Equal(t, int32(-33), got.Exponent())

	beyond := oneWithTrailingZeros(1234)
	require.Greater(t, beyond.Coefficient().BitLen(), decimal128.MaxCoefficientBits)
	require.False(t, decimal128.Fits(beyond))
}

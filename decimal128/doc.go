// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package decimal128 tells whether a [decimal.Decimal] is held exactly by an
// IEEE 754-2008 decimal128 (the BSON Decimal128 of MongoDB), and returns the
// form a decimal128 stores it in, in pure Go and without a database driver.
// Nothing is ever rounded: a decimal either fits exactly or is refused.
//
// The filter package bounds decimal literals with it, the filter evaluator
// sizes its decimal work by its range, and the MongoDB backend's decimal
// codec stores exactly the decimals [Fits] accepts. Validate input with [Fits]
// to refuse a value before it reaches a store.
//
// # Range
//
// A decimal128 holds a coefficient of at most [MaxDigits] significant digits
// with an exponent in [[MinExp], [MaxExp]]. Trailing zeros of the coefficient
// fold into the exponent, and a short coefficient may be padded to bring a
// large exponent down, so the range is judged on the normalized form:
//
//	digits, exp := decimal128.Normalize(d) // trailing zeros folded
//	decimal128.Representable(digits, exp)  // the range check of decimal128.Fits(d)
//
// # Canonical form
//
// [Canonical] returns a fitting decimal exactly as a decimal128 stores it and
// reads it back — the MongoDB driver's conversion, rounding nothing: the
// coefficient is shortened only as far as needed to reach 34 digits or the
// minimum exponent, and padded only as far as needed to reach the maximum
// exponent; a zero is decimal.Zero. Comparing, rescaling or printing the
// canonical form takes bounded work whatever exponent the input had.
//
//	c, ok := decimal128.Canonical(decimal.New(1, 6144)) // 1000…0 (34 digits) E6111, true
//
// # Untrusted input
//
// [Fits] and [Canonical] refuse a coefficient of more than
// [MaxCoefficientBits] bits without copying or formatting it, so they take
// bounded time on any decimal.Decimal, a coefficient of millions of digits
// included. [Normalize] formats the coefficient; bound it with
// [CoefficientWithinLimit] before calling [Normalize] on untrusted input.
// [SmallCoefficient] is a cheap bit-width check for pricing the work on a
// decimal; it does not tell whether a decimal128 holds the value, [Fits] does.
package decimal128

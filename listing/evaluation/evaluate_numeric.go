// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"cmp"
	"math"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func numericFloat(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// maxExactInt is the largest magnitude below which every integer is exact in a float64.
const maxExactInt = 1 << 53

// numericOrderFast compares int64 and float64 values without big.Rat: two integers directly, two floats directly, and an
// integer with a float when the integer is exact in a float64. handled is false for the other cases, which take the exact path.
func numericOrderFast(a, b any) (c int, ok, handled bool) {
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64:
			return cmp.Compare(x, y), true, true
		case float64:
			if c, ok, handled = compareIntFloat(x, y); handled {
				return c, ok, true
			}
		}
	case float64:
		switch y := b.(type) {
		case float64:
			if !finite(x) || !finite(y) {
				return 0, false, true
			}

			return compareFinite(x, y), true, true
		case int64:
			if c, ok, handled = compareIntFloat(y, x); handled {
				return -c, ok, true
			}
		}
	}

	return 0, false, false
}

// compareIntFloat compares an integer with a float when the integer is exact in a float64. A non-finite float is not a number of
// the exact order, as in numericRat.
func compareIntFloat(i int64, f float64) (c int, ok, handled bool) {
	if !finite(f) {
		return 0, false, true
	}

	if i > maxExactInt || i < -maxExactInt {
		return 0, false, false
	}

	return compareFinite(float64(i), f), true, true
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func compareFinite(x, y float64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	default:
		return 0
	}
}

func numericOrder(a, b any) (int, bool) {
	if c, ok, handled := numericOrderFast(a, b); handled {
		return c, ok
	}

	_, ad := a.(decimal.Decimal)

	_, bd := b.(decimal.Decimal)
	if ad || bd {
		// Reuse the cursor's exact, exponent-bounded comparison. uint64 values
		// beyond int64 are converted without passing through float64.
		if u, ok := a.(uint64); ok {
			a = decimal.NewFromBigInt(new(big.Int).SetUint64(u), 0)
		}

		if u, ok := b.(uint64); ok {
			b = decimal.NewFromBigInt(new(big.Int).SetUint64(u), 0)
		}

		left, err := pagetoken.NewSortValue(a)
		if err != nil || left.Class() != pagetoken.SortClassNumber {
			return 0, false
		}

		right, err := pagetoken.NewSortValue(b)
		if err != nil || right.Class() != pagetoken.SortClassNumber {
			return 0, false
		}

		return left.Compare(right), true
	}

	left, ok := numericRat(a)
	if !ok {
		return 0, false
	}

	right, ok := numericRat(b)
	if !ok {
		return 0, false
	}

	return left.Cmp(right), true
}

func numericRat(value any) (*big.Rat, bool) {
	if integer, ok := value.(int64); ok {
		return new(big.Rat).SetInt64(integer), true
	}

	if integer, ok := value.(uint64); ok {
		return new(big.Rat).SetUint64(integer), true
	}

	if number, ok := numericFloat(value); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}

		return new(big.Rat).SetFloat64(number), true
	}

	return nil, false
}

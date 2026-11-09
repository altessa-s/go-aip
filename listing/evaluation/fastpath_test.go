// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ratOrder is the exact reference order of numericOrder for int64, uint64 and float64 values.
func ratOrder(a, b any) (int, bool) {
	rat := func(v any) (*big.Rat, bool) {
		switch n := v.(type) {
		case int64:
			return new(big.Rat).SetInt64(n), true
		case uint64:
			return new(big.Rat).SetUint64(n), true
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, false
			}

			return new(big.Rat).SetFloat64(n), true
		default:
			return nil, false
		}
	}

	x, ok := rat(a)
	if !ok {
		return 0, false
	}

	y, ok := rat(b)
	if !ok {
		return 0, false
	}

	return x.Cmp(y), true
}

func TestNumericOrderMatchesExactOrder(t *testing.T) {
	t.Parallel()

	const exact = int64(1) << 53

	values := []any{
		int64(0), int64(1), int64(-1), exact, exact + 1, -exact, -exact - 1, int64(math.MaxInt64), int64(math.MinInt64),
		float64(0), math.Copysign(0, -1), 0.5, -0.5, float64(exact), float64(exact) + 2, float64(-exact) - 2, 9.3e18, -9.3e18,
		math.MaxFloat64, math.SmallestNonzeroFloat64, math.NaN(), math.Inf(1), math.Inf(-1), uint64(math.MaxUint64), uint64(3),
	}

	for _, a := range values {
		for _, b := range values {
			wantC, wantOK := ratOrder(a, b)
			gotC, gotOK := numericOrder(a, b)
			require.Equal(t, wantOK, gotOK, "%#v vs %#v", a, b)
			require.Equal(t, wantC, gotC, "%#v vs %#v", a, b)
		}
	}
}

// reflectNormalize is the reflection-only normalization the built-in fast path must reproduce.
func reflectNormalize(value any) any {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}

	if timestamp, ok := reflect.TypeAssert[time.Time](rv); ok {
		return timestamp.UTC()
	}

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Slice:
		if rv.IsNil() {
			return nil
		}

		return rv.Bytes()
	default:
		return rv.Interface()
	}
}

func TestNormalizeBuiltinMatchesReflection(t *testing.T) {
	t.Parallel()

	local := time.Date(2026, 3, 1, 12, 0, 0, 5, time.FixedZone("x", 3600))
	values := []any{
		nil, "a", true, int64(-3), uint64(7), 1.5, decimal.RequireFromString("1.20"), local, local.UTC(), time.Now(),
		[]byte("b"), []byte(nil), int(-1), int8(-2), int16(-3), int32(-4), uint(1), uint8(2), uint16(3), uint32(4), float32(0.25),
	}

	for _, v := range values {
		got, ok := normalizeBuiltin(v)
		require.True(t, ok, "%T", v)

		want := reflectNormalize(v)
		if at, isTime := want.(time.Time); isTime {
			require.True(t, at.Equal(got.(time.Time)), "%v", v)
			require.Equal(t, time.UTC, got.(time.Time).Location())

			continue
		}

		require.Equal(t, want, got, "%T", v)
		require.Equal(t, reflect.TypeOf(want), reflect.TypeOf(got), "%T", v)
	}

	type named string

	_, ok := normalizeBuiltin(named("a"))
	require.False(t, ok)
	require.Equal(t, "a", normalizeScalar(named("a")))
}

func TestEqualFastPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b any
		want bool
	}{
		{a: "a", b: "a", want: true},
		{a: "a", b: "b"},
		{a: "1", b: int64(1)},
		{a: true, b: true, want: true},
		{a: true, b: false},
		{a: true, b: int64(1)},
		{a: int64(2), b: 2.0, want: true},
		{a: math.Inf(1), b: math.Inf(1), want: true},
		{a: math.NaN(), b: math.NaN()},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, equal(tt.a, tt.b), "%#v == %#v", tt.a, tt.b)
		require.Equal(t, tt.want, equal(tt.b, tt.a), "%#v == %#v", tt.b, tt.a)
	}
}

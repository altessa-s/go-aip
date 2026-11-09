// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
)

var errType = errors.New("type error")

// budget is a [sqltype.Budget] with left units.
type budget struct{ left int }

func (b *budget) Spend(n int) error {
	if n < 0 || n > b.left {
		return filter.ErrDataLimitExceeded
	}

	b.left -= n

	return nil
}

func (*budget) TypeError() error { return errType }

type celsius float32

type count uint16

func TestInferKind(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value any
		want  sqlbase.Kind
	}{
		{true, sqlbase.Boolean},
		{"a", sqlbase.Text},
		{[]byte("a"), sqlbase.Bytes},
		{time.Unix(0, 0), sqlbase.Timestamp},
		{decimal.New(1, 0), sqlbase.Numeric},
		{json.Number("1"), sqlbase.Numeric},
		{1.5, sqlbase.Float},
		{float32(1.5), sqlbase.Float},
		{int8(1), sqlbase.Integer},
		{uint64(1), sqlbase.Integer},
		{nil, sqlbase.Unknown},
		{struct{}{}, sqlbase.Unknown},
		{celsius(1), sqlbase.Unknown},
	} {
		assert.Equal(t, tc.want, sqltype.InferKind(tc.value), "%T", tc.value)
	}
}

func TestParseNumber(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		value   any
		want    string
		special int
		err     error
	}{
		{name: "number", value: sqltype.Number{Special: sqltype.NaN}, special: sqltype.NaN},
		{name: "decimal", value: decimal.RequireFromString("12.50"), want: "12.5"},
		{name: "json number", value: json.Number("-3.25"), want: "-3.25"},
		{name: "string", value: " 7 ", want: "7"},
		{name: "int64", value: int64(-9), want: "-9"},
		{name: "uint64", value: uint64(math.MaxUint64), want: "18446744073709551615"},
		{name: "float64", value: 0.5, want: "0.5"},
		{name: "named float", value: celsius(2.5), want: "2.5"},
		{name: "named uint", value: count(4), want: "4"},
		{name: "int", value: 3, want: "3"},
		{name: "nan", value: "NaN", special: sqltype.NaN},
		{name: "infinity", value: "+Infinity", special: sqltype.PositiveInfinity},
		{name: "inf", value: "inf", special: sqltype.PositiveInfinity},
		{name: "negative infinity", value: "-inf", special: sqltype.NegativeInfinity},
		{name: "not a number", value: "x", err: errType},
		{name: "unsupported type", value: true, err: errType},
		{name: "nil", value: nil, err: errType},
		{name: "decimal exponent too large", value: decimal.New(1, sqltype.MaxIntegerDigits+1), err: errType},
		{name: "decimal exponent too small", value: decimal.New(1, -sqltype.MaxFractionDigits-1), err: errType},
		{name: "too many integer digits", value: "1e131072", err: errType},
		{name: "too many fraction digits", value: "1e-16384", err: errType},
		{name: "text too long", value: strings.Repeat("1", sqltype.MaxIntegerDigits+sqltype.MaxFractionDigits+33), err: errType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n, err := sqltype.ParseNumber(tc.value, &budget{left: 1 << 20})
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.special, n.Special)

			if tc.special == 0 {
				assert.Equal(t, tc.want, n.Finite.String())
			}
		})
	}
}

func TestParseNumberBudget(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value any
		left  int
	}{
		{name: "decimal digits", value: decimal.RequireFromString("123.45"), left: 3},
		{name: "text", value: "12345", left: 4},
		{name: "parsed digits", value: "12345", left: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := sqltype.ParseNumber(tc.value, &budget{left: tc.left})
			require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
		})
	}
}

func TestCompareNumber(t *testing.T) {
	t.Parallel()

	one := sqltype.Number{Finite: decimal.New(1, 0)}
	two := sqltype.Number{Finite: decimal.New(2, 0)}
	neg := sqltype.Number{Special: sqltype.NegativeInfinity}
	pos := sqltype.Number{Special: sqltype.PositiveInfinity}
	nan := sqltype.Number{Special: sqltype.NaN}

	for _, tc := range []struct {
		a, b sqltype.Number
		want int
	}{
		{one, two, -1},
		{two, one, 1},
		{one, one, 0},
		{neg, one, -1},
		{pos, one, 1},
		{pos, nan, -1},
		{nan, pos, 1},
		{nan, nan, 0},
	} {
		assert.Equal(t, tc.want, sqltype.CompareNumber(tc.a, tc.b))
	}
}

func TestInteger(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value any
		want  any
		err   error
	}{
		{value: "42", want: int64(42)},
		{value: decimal.RequireFromString("-7.0"), want: int64(-7)},
		{value: "1.5", err: errType},
		{value: "NaN", err: errType},
		{value: "9223372036854775808", err: errType},
		{value: "-9223372036854775809", err: errType},
		{value: true, err: errType},
	} {
		got, err := sqltype.Integer(tc.value, &budget{left: 1 << 10})
		if tc.err != nil {
			require.ErrorIs(t, err, tc.err, "%v", tc.value)
			continue
		}

		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestFloat(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value any
		want  float64
		err   error
	}{
		{value: 1.25, want: 1.25},
		{value: float32(0.5), want: 0.5},
		{value: "2.5", want: 2.5},
		{value: "-Infinity", want: math.Inf(-1)},
		{value: "Infinity", want: math.Inf(1)},
		{value: "1e400", err: errType},
		{value: "1e-400", err: errType},
		{value: "0", want: 0},
		{value: "x", err: errType},
	} {
		got, err := sqltype.Float(tc.value, &budget{left: 1 << 10})
		if tc.err != nil {
			require.ErrorIs(t, err, tc.err, "%v", tc.value)
			continue
		}

		require.NoError(t, err)
		assert.InDelta(t, tc.want, got, 0)
	}

	got, err := sqltype.Float("NaN", &budget{left: 10})
	require.NoError(t, err)
	assert.True(t, math.IsNaN(got.(float64)))
}

func TestUnwrap(t *testing.T) {
	t.Parallel()

	five := 5
	ptr := &five

	var (
		nilPtr *int
		nilMap map[string]int
	)

	type wrapped struct{ v any }

	decode := func(v any) (any, error) {
		switch x := v.(type) {
		case wrapped:
			return x.v, nil
		case string:
			if x == "null" {
				return nil, nil
			}

			if x == "bad" {
				return nil, errType
			}
		}

		return v, nil
	}

	for _, tc := range []struct {
		name   string
		value  any
		depth  int
		decode sqltype.Decoder
		want   any
		err    error
	}{
		{name: "value", value: 3, depth: 4, want: 3},
		{name: "nil", value: nil, depth: 4},
		{name: "nil pointer", value: nilPtr, depth: 4},
		{name: "nil map", value: nilMap, depth: 4},
		{name: "pointers", value: &ptr, depth: 4, want: 5},
		{name: "too deep", value: &ptr, depth: 2, err: filter.ErrDataLimitExceeded},
		{name: "decoded", value: wrapped{v: &five}, depth: 4, decode: decode, want: 5},
		{name: "decoded null", value: "null", depth: 4, decode: decode},
		{name: "decoder error", value: "bad", depth: 4, decode: decode, err: errType},
		{name: "decoded same type", value: "text", depth: 4, decode: decode, want: "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := sqltype.Unwrap(tc.value, tc.depth, &budget{left: 100}, tc.decode)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := sqltype.Unwrap(&ptr, 8, &budget{left: 2}, nil)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

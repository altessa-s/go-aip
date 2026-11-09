// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// exactNumberCase is an expression evaluated against one value of n.
type exactNumberCase struct {
	expr string
	n    any
	want bool
}

// integerFloatCases compare integers with floats around 2^53 and the int64 and uint64 limits, where float64 cannot hold every
// integer: the integer is compared exactly, never rounded to the float. Both evaluators agree on them.
func integerFloatCases() []exactNumberCase {
	const (
		twoTo53     = int64(1) << 53
		twoTo53Plus = twoTo53 + 1
	)

	return []exactNumberCase{
		// 2^53+1 rounds to the float 2^53.
		{`n == 9007199254740992.0`, twoTo53Plus, false},
		{`n != 9007199254740992.0`, twoTo53Plus, true},
		{`n > 9007199254740992.0`, twoTo53Plus, true},
		{`n >= 9007199254740992.0`, twoTo53Plus, true},
		{`n < 9007199254740992.0`, twoTo53Plus, false},
		{`n == 9007199254740992.0`, twoTo53, true},
		{`n in [9007199254740992.0]`, twoTo53Plus, false},
		{`n in [9007199254740992.0]`, twoTo53, true},
		{`n in [1.5, 9007199254740993]`, twoTo53Plus, true},
		// A float operand in the data against an integer literal.
		{`n == 9007199254740993`, float64(twoTo53), false},
		{`n < 9007199254740993`, float64(twoTo53), true},
		{`n in [9007199254740993]`, float64(twoTo53), false},
		{`n == 9007199254740992`, float64(twoTo53), true},
		// Fractions.
		{`n < 2.5`, int64(2), true},
		{`n > -2.5`, int64(-2), true},
		{`n == 2.0`, int64(2), true},
		{`n > 1.5`, uint64(2), true},
		{`n > -0.5`, uint64(0), true},
		// The int64 limits: MaxInt64 rounds to the float 2^63, MinInt64 is -2^63 exactly.
		{`n == 9223372036854775808.0`, int64(math.MaxInt64), false},
		{`n < 9223372036854775808.0`, int64(math.MaxInt64), true},
		{`n == -9223372036854775808.0`, int64(math.MinInt64), true},
		{`n > -9223372036854775808.0`, int64(math.MinInt64), false},
		{`n < -9223372036854775808.0`, int64(math.MinInt64 + 1), false},
		// The uint64 limits: MaxUint64 rounds to the float 2^64.
		{`n == 18446744073709551616.0`, uint64(math.MaxUint64), false},
		{`n < 18446744073709551616.0`, uint64(math.MaxUint64), true},
		{`n > 18446744073709549568.0`, uint64(math.MaxUint64), true},
		{`n == 9223372036854775808.0`, uint64(1) << 63, true},
		{`n == 18446744073709551615u`, float64(1 << 64), false},
		{`n < 18446744073709551615u`, float64(1 << 64), false},
		// Infinities order beyond every integer.
		{`n < 9223372036854775807`, math.Inf(1), false},
		{`n > 9223372036854775807`, math.Inf(1), true},
		{`n > 18446744073709551615u`, math.Inf(1), true},
		{`n < -9223372036854775808`, math.Inf(-1), true},
		{`n == 0`, math.Inf(-1), false},
		{`n < 0u`, math.Inf(-1), true},
	}
}

// TestEvaluator_ExactIntegerFloat: the default evaluator compares an integer with a float exactly.
func TestEvaluator_ExactIntegerFloat(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator()
	require.NoError(t, err)

	cases := append(integerFloatCases(),
		// NaN keeps the IEEE 754 comparison: it equals nothing, itself included, and orders against nothing.
		exactNumberCase{`n == 1`, math.NaN(), false},
		exactNumberCase{`n != 1`, math.NaN(), true},
		exactNumberCase{`n < 9223372036854775807`, math.NaN(), false},
		exactNumberCase{`n >= 0u`, math.NaN(), false},
		exactNumberCase{`n in [1, 2.5]`, math.NaN(), false},
	)
	runExactNumbers(t, eval, "n", cases)
}

// runExactNumbers evaluates every case, each in its own subtest, against a document holding its value in field.
func runExactNumbers(t *testing.T, eval *filtereval.Evaluator, field string, cases []exactNumberCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(fmt.Sprintf("%s/%T(%v)", tt.expr, tt.n, tt.n), func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), map[string]any{field: tt.n})
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestEvaluator_IntegerFloatOrderIsExact checks every comparison operator, in both evaluators and both operand orders, on integers
// and floats at the edges of float64 precision and of the integer ranges against the order of their exact values.
func TestEvaluator_IntegerFloatOrderIsExact(t *testing.T) {
	t.Parallel()

	integers := []any{
		int64(math.MinInt64), int64(math.MinInt64 + 1), -int64(1<<53) - 1, -int64(1 << 53), int64(-1), int64(0), int64(1),
		int64(1 << 53), int64(1<<53 + 1), int64(math.MaxInt64 - 1), int64(math.MaxInt64),
		uint64(0), uint64(1), uint64(1<<53 + 1), uint64(1 << 63), uint64(1<<63 + 1), uint64(math.MaxUint64 - 1), uint64(math.MaxUint64),
	}
	floats := []float64{
		math.Inf(-1), -math.MaxFloat64, -0x1p64, -0x1p63 - 2048, -0x1p63, -0x1p53, -1.5, -0.5, math.Copysign(0, -1), 0,
		math.SmallestNonzeroFloat64, 0.5, 1, 1.5, 0x1p53, 0x1p53 + 2, 0x1p63 - 1024, 0x1p63, 0x1p64 - 2048, 0x1p64,
		math.MaxFloat64, math.Inf(1),
	}
	exact := func(v any) *big.Float {
		switch n := v.(type) {
		case int64:
			return new(big.Float).SetInt64(n)
		case uint64:
			return new(big.Float).SetUint64(n)
		default:
			return new(big.Float).SetFloat64(n.(float64)) //nolint:forcetypeassert // the fixtures are numbers
		}
	}
	ops := []struct {
		op    filter.Operator
		holds func(c int) bool
	}{
		{filter.OpEqual, func(c int) bool { return c == 0 }},
		{filter.OpNotEqual, func(c int) bool { return c != 0 }},
		{filter.OpLT, func(c int) bool { return c < 0 }},
		{filter.OpLTE, func(c int) bool { return c <= 0 }},
		{filter.OpGT, func(c int) bool { return c > 0 }},
		{filter.OpGTE, func(c int) bool { return c >= 0 }},
	}
	plain, err := newEvaluator()
	require.NoError(t, err)

	evaluators := map[string]*filtereval.Evaluator{"default": plain}

	for name, eval := range evaluators {
		for _, i := range integers {
			for _, f := range floats {
				for _, pair := range [][2]any{{i, f}, {f, i}} {
					want := exact(pair[0]).Cmp(exact(pair[1]))
					for _, o := range ops {
						node := &filter.BinaryOpNode{Op: o.op, Left: &filter.IdentNode{Name: "n"}, Right: &filter.LiteralNode{Value: pair[1]}}
						got, err := eval.Evaluate(node, map[string]any{"n": pair[0]})
						require.NoError(t, err)
						require.Equal(t, o.holds(want), got, "%s: %T(%v) %v %T(%v)", name, pair[0], pair[0], o.op, pair[1], pair[1])
					}
				}
			}
		}
	}
}

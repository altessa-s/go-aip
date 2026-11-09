// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"math/big"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// The tests of this file check that no work proportional to the size of a data value happens before it is charged to the data
// budget or bounded by a fixed cap: each evaluates a value whose size would make that work take seconds or gigabytes, and bounds
// the time and the bytes the evaluation allocates. They measure allocations, so they do not run in parallel.

// Bounds of an evaluation that must not depend on the size of its data.
const (
	quickEvaluation = raceSlowdown * time.Second // the shapes measured take seconds or more without their bound (raceSlowdown times that under -race)
	smallAllocation = 1 << 20
)

// measuredRuns is how many times [measured] runs a function: the bytes allocated are read process-wide, so activity left by
// earlier tests may add to one run, unlikely to all of them; the work bounded here takes megabytes and seconds in every run.
const measuredRuns = 3

// measured runs fn [measuredRuns] times and returns the least time a run took and the least bytes one allocated. fn must give the
// same result every run.
func measured(fn func()) (elapsed time.Duration, allocated uint64) {
	for run := range measuredRuns {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		start := time.Now()

		fn()

		took := time.Since(start)

		runtime.ReadMemStats(&after)

		if bytes := after.TotalAlloc - before.TotalAlloc; run == 0 || bytes < allocated {
			allocated = bytes
		}

		if run == 0 || took < elapsed {
			elapsed = took
		}
	}

	return elapsed, allocated
}

// requireNoMatch asserts an evaluation that matched nothing: false, or under the default semantics the error of comparing values
// of different types (an opaque value with a literal).
func requireNoMatch(t *testing.T, got bool, err error, msgAndArgs ...any) {
	t.Helper()

	if err != nil {
		require.ErrorIs(t, err, filter.ErrUnsupportedType, msgAndArgs...)
	}

	require.False(t, got, msgAndArgs...)
}

// TestEvaluator_OpaqueValuesNotCompared: review finding R13-1 — `v in [v]` with v a [1<<30]struct{} compared the two operands of
// the same type through reflect.Value.Comparable, which visits every element of an array of structs: a billion iterations after
// one charged unit (verified on the unfixed code: the evaluation takes seconds). The default evaluator's `in` compares two values
// of a type it does not know as Go's == does, as it always has, without walking what == does not depend on: an array or a struct
// holding neither an interface nor a string is compared by ==, in time independent of its length; an interface inside is
// comparable by the dynamic value it holds. An opaque value equals nothing under MongoDB semantics.
func TestEvaluator_OpaqueValuesNotCompared(t *testing.T) { //nolint:paralleltest // measures allocations
	huge := [1 << 30]struct{}{}
	holder := struct{ A [1 << 30]struct{} }{}

	type ifaceHolder struct {
		A [1 << 30]struct{}
		I any
	}

	iface, slice := ifaceHolder{I: int64(1)}, ifaceHolder{I: []int{1}}
	data := map[string]any{
		"v": huge, "s": holder, "i": iface, "j": slice, "w": []any{huge, holder}, "t": []any{holder}, "u": []any{iface}, "x": []any{slice},
	}
	plain, err := newEvaluator()
	require.NoError(t, err)

	for expr, want := range map[string]bool{
		"v in [v]": true, "s in [s]": true, "v in w": true, "s in t": true, "v in [s, v]": true, "s in [1, s]": true,
		"v in [s]": false, "s in [v]": false, "i in [i]": true, "i in u": true, "j in [j]": false, "j in x": false, "i in x": false,
	} {
		node := filtertest.MustParseFilter(t, expr)

		var got bool

		elapsed, allocated := measured(func() { got, err = plain.Evaluate(node, data) })

		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
		requireQuick(t, elapsed, quickEvaluation, expr)
		require.Less(t, allocated, uint64(smallAllocation), expr)
	}

	elapsed, _ := measured(func() { _, err = plain.Evaluate(filtertest.MustParseFilter(t, "v == v"), data) })

	require.ErrorIs(t, err, filter.ErrUnsupportedType)
	requireQuick(t, elapsed, quickEvaluation)

	for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields("w"))) {
		for _, expr := range []string{"v in [1, 2]", "w == 1", "1 in w"} {
			var got bool

			elapsed, _ := measured(func() { got, err = eval.Evaluate(filtertest.MustParseFilter(t, expr), data) })

			requireNoMatch(t, got, err, "%s: %s", mode, expr)
			requireQuick(t, elapsed, quickEvaluation, "%s: %s", mode, expr)
		}
	}

	// Comparable values of a type the evaluator does not know are still members of a list holding them; values of two Go types are
	// not, for the default evaluator, whatever they hold. (objectID has the shape of the MongoDB driver's ObjectID.)
	type objectID [12]byte

	scalars := map[string]any{"v": objectID{1}, "n": int8(3), "w": []any{objectID{1}, int16(3)}}
	for expr, want := range map[string]bool{"v in w": true, "n in w": false} {
		got, err := plain.Evaluate(filtertest.MustParseFilter(t, expr), scalars)
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

// TestEvaluator_OversizedDecimalBounded: review finding R13-3 — a decimal.Decimal of a 200,001-digit coefficient (10^200000 at
// exponent -200000, exactly 1) was copied whole before its size was checked, on every comparison, and inside a struct the codec
// divided it by ten 200,000 times to fit a Decimal128 (verified on the unfixed code: seconds); a decimal map key spelled out its
// exponent. Such a coefficient is refused before it is copied, directly or nested in a struct, an Optional, a NullDecimal, a
// pointer or a slice, and a map keyed by a decimal is opaque.
func TestEvaluator_OversizedDecimalBounded(t *testing.T) { //nolint:paralleltest // measures allocations
	const digits = 200_000

	huge := decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(digits), nil), -digits)

	values := map[string]any{
		"direct": huge,
		"struct": struct{ D decimal.Decimal }{D: huge},
		"optional": struct {
			D optional.Optional[decimal.Decimal]
		}{D: optional.Some(huge)},
		"null decimal": struct{ D decimal.NullDecimal }{D: decimal.NullDecimal{Decimal: huge, Valid: true}},
		"pointer":      struct{ D *decimal.Decimal }{D: &huge},
		"slice":        []decimal.Decimal{huge, huge},
		"map key":      map[decimal.Decimal]int64{decimal.New(1, 1<<24): 1},
	}
	for name, v := range values {
		data := map[string]any{"v": v}

		for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields("v", "v.d"))) {
			for _, expr := range []string{"v == 1", "v > 0", "v in [1, 2]", "has(v.d)", "v.d == 1"} {
				node := filtertest.MustParseFilter(t, expr)

				var (
					got bool
					err error
				)

				elapsed, allocated := measured(func() { got, err = eval.Evaluate(node, data) })

				require.NotErrorIs(t, err, filter.ErrDataLimitExceeded, "%s %s: %s", name, mode, expr)
				require.False(t, got, "%s %s: %s", name, mode, expr)
				requireQuick(t, elapsed, quickEvaluation, "%s %s: %s", name, mode, expr)
				require.Less(t, allocated, uint64(32<<10), "%s %s: %s", name, mode, expr)
			}
		}
	}

	// A coefficient within the bound is read wherever it is. (Without a value encoding a struct is opaque, so the nested one is
	// held by a map; the MongoDB encoding's struct case is in the value encoding tests.)
	one := decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(1000), nil), -1000)
	for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields("w.d"))) {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, "v == 1 && w.d == 1"),
			map[string]any{"v": one, "w": map[string]any{"d": one}})
		require.NoError(t, err, mode)
		require.True(t, got, mode)
	}
}

// wideStruct returns a struct type constructed at run time, unknown to any cache, of n exported fields of type struct{}, field i
// keyed "k<i>" and tagged with options more options.
func wideStruct(n, options int) reflect.Type {
	fields := make([]reflect.StructField, n)
	prefix := "F" + strconv.FormatInt(time.Now().UnixNano(), 36) + "x"

	for i := range fields {
		tag := `bson:"k` + strconv.Itoa(i) + strings.Repeat(",o", options) + `"`
		fields[i] = reflect.StructField{Name: prefix + strconv.Itoa(i), Type: reflect.TypeFor[struct{}](), Tag: reflect.StructTag(tag)}
	}

	return reflect.StructOf(fields)
}

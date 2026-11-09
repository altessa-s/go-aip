// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestReader_ResolvePathBudget sweeps the data budget over a path that crosses a list of documents: every budget either resolves
// the full answer or, once exhausted, nothing, and Err reports the exhausted budget.
func TestReader_ResolvePathBudget(t *testing.T) {
	t.Parallel()

	data := map[string]any{"a": []any{
		map[string]any{"b": int64(1)},
		"not a document",
		map[string]any{"c": int64(2)},
		map[string]any{"b": []any{int64(3)}},
	}}

	var full evaluator.Reader

	full.Reset(data, 1<<20, nil)
	want, present, crossed := full.ResolvePath(full.Data(), "a.b")
	full.Release()
	require.True(t, present)
	require.True(t, crossed)
	require.Equal(t, []any{int64(1), []any{int64(3)}}, want)

	sawExhausted := false

	for left := range 64 {
		var r evaluator.Reader

		r.Reset(data, left, nil)
		v, ok, inner := r.ResolvePath(data, "a.b")

		if r.Exhausted() {
			sawExhausted = true

			require.ErrorIs(t, r.Err(), filter.ErrDataLimitExceeded, "budget %d", left)
			require.Nil(t, v, "budget %d", left)
			require.False(t, ok, "budget %d", left)
			require.False(t, inner, "budget %d", left)

			continue
		}

		require.NoError(t, r.Err())
		require.Equal(t, want, v, "budget %d", left)
		require.True(t, ok)
		require.True(t, inner)
	}

	require.True(t, sawExhausted)
}

// TestReader_StoredForms pins the reading of a Reader: values in their stored form, paths stepping through stored documents,
// QueryValues traversing lists of documents and flattening a list at the end, and the charges of the budget.
func TestReader_StoredForms(t *testing.T) {
	t.Parallel()

	type named string

	data := map[string]any{
		"doc":   map[string]string{"name": "alpha"},
		"tags":  []string{"red", "green"},
		"steps": []any{map[string]any{"codes": []any{"x", "y"}}, map[string]any{"other": int64(1)}, "skip"},
		"label": named("tool"),
		"nil":   []int(nil),
	}

	var r evaluator.Reader

	r.Reset(data, 1<<20, nil)
	defer r.Release()

	require.Nil(t, r.Encoding())
	require.Len(t, r.Data(), len(data))
	require.Equal(t, "tool", r.Stored(named("tool")))
	require.Nil(t, r.Stored([]int(nil)), "a nil slice is stored as null")

	v, present := r.WalkField("doc.name")
	require.True(t, present)
	require.Equal(t, "alpha", v, "a typed map is a stored document")

	_, present = r.WalkField("doc.missing")
	require.False(t, present)
	require.Equal(t, "alpha", r.ReadPath("doc.name"))
	require.Equal(t, []any{"red", "green"}, r.ReadPath("tags"))
	require.Nil(t, r.ReadPath("nil"))

	tags, present, crossed := r.ResolvePath(r.Data(), "tags")
	require.True(t, present)
	require.False(t, crossed)
	require.Equal(t, []any{"red", "green"}, tags)

	require.Equal(t, []any{"red", "green"}, r.QueryValues(r.Data(), "tags", true, nil), "a list at the end is flattened")
	require.Equal(t, []any{[]any{"red", "green"}}, r.QueryValues(r.Data(), "tags", false, nil))
	require.Equal(t, []any{"x", "y", nil}, r.QueryValues(r.Data(), "steps.codes", true, nil),
		"documents of a list are traversed, one lacking the path yields null, other elements nothing")
	require.Equal(t, []any{nil}, r.QueryValues(r.Data(), "missing.path", true, nil))
	require.Equal(t, []any{nil}, r.QueryValues(r.Data(), "label.x", true, nil), "a scalar on the way yields null")

	require.True(t, r.Charge(1))
	require.True(t, r.ChargeScan(10))
	require.True(t, r.ChargeRegex(10, 10))
	require.True(t, r.ChargeComparison("a", "b"))
	require.False(t, r.Exhausted())
	require.NoError(t, r.Err())

	var small evaluator.Reader

	small.Reset(data, 2, nil)
	require.False(t, small.ChargeScan(1<<20))
	require.True(t, small.Exhausted())
	require.False(t, small.Charge(1), "every later charge fails once the budget is exhausted")
	require.ErrorIs(t, small.Err(), filter.ErrDataLimitExceeded)
	small.Release()
	require.Nil(t, small.Data())
}

// TestGuard pins the operation and depth limits of a Guard and their errors.
func TestGuard(t *testing.T) {
	t.Parallel()

	var g evaluator.Guard

	g.Reset(3, 1)
	require.NoError(t, g.Enter())
	require.ErrorIs(t, g.Enter(), filter.ErrMaxDepthExceeded)
	g.Leave()
	require.NoError(t, g.Op())
	require.ErrorIs(t, g.Op(), filter.ErrMaxOperationsExceeded)

	g.Reset(1, 1)
	require.NoError(t, g.Op())
	require.ErrorIs(t, g.Enter(), filter.ErrMaxOperationsExceeded)
}

// TestCompareHelpers pins the comparison building blocks a profile shares with the evaluator.
func TestCompareHelpers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		op   filter.Operator
		c    int
		want bool
	}{
		{filter.OpEqual, 0, true}, {filter.OpNotEqual, 0, false}, {filter.OpLT, -1, true}, {filter.OpLTE, 0, true},
		{filter.OpGT, 1, true}, {filter.OpGTE, -1, false},
	} {
		got, err := evaluator.CompareResult(tc.op, tc.c)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%v %d", tc.op, tc.c)
	}

	_, err := evaluator.CompareResult(filter.OpAnd, 0)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)

	c, ok, err := evaluator.CompareExact(int64(1<<53+1), float64(1<<53))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, c, "an integer is never rounded to a float64")

	c, ok, err = evaluator.CompareExact(decimal.RequireFromString("1.50"), "1.5")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 0, c)

	_, ok, err = evaluator.CompareExact(decimal.RequireFromString("1"), true)
	require.True(t, ok)
	require.Error(t, err, "a decimal against a value that is no decimal")

	_, ok, _ = evaluator.CompareExact("a", "b")
	require.False(t, ok)

	_, ok, _ = evaluator.CompareExact(math.NaN(), 1.0)
	require.False(t, ok)
}

// TestLiteralPatterns pins the precompiled matches() patterns: each literal pattern once, a pattern the policy rejects and a
// pattern read from a field left out, nil without any.
func TestLiteralPatterns(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	node := filtertest.MustParseFilter(t, `a.matches("^x") || b.matches("^x") || c.matches("(") || d.matches(e)`)

	patterns := evaluator.LiteralPatterns(policy, node)
	require.Len(t, patterns, 1)
	require.True(t, patterns["^x"].MatchString("xy"))
	require.Nil(t, evaluator.LiteralPatterns(policy, filtertest.MustParseFilter(t, `a == 1`)))
}

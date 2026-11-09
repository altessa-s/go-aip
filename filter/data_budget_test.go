// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// evaluators returns the default evaluator with opts by mode name; backend/mongo runs the same cases under the MongoDB profile.
func evaluators(t *testing.T, opts ...any) map[string]*filtereval.Evaluator {
	t.Helper()

	plain, err := newEvaluator(opts...)
	require.NoError(t, err)

	return map[string]*filtereval.Evaluator{"default": plain}
}

// TestEvaluator_DataBudgetSharedPaths: the cases of review finding R12-3 — a map holding a list of itself, and a DAG of maps sharing
// their lists, double the values a dotted path reaches at every step, so a 30-step path reaches 2^30 of them (verified on the unfixed
// code: the evaluation does not finish). Every predicate on such a path fails with ErrDataLimitExceeded in bounded time, in both
// modes: comparisons, in, has(), size() and the string functions.
func TestEvaluator_DataBudgetSharedPaths(t *testing.T) {
	t.Parallel()

	const steps = 30

	path := "a" + strings.Repeat(".a", steps-1)
	cyclic := map[string]any{}
	cyclic["a"] = []any{cyclic, cyclic}

	dag := map[string]any{"a": int64(1)}
	for range steps {
		dag = map[string]any{"a": []any{dag, dag}}
	}

	exprs := []string{
		path + " == 1", path + " != 1", path + " in [1, 2]", "has(" + path + ")", "size(" + path + ") == 1", "size(" + path + ") > 1",
		path + `.startsWith("x")`, path + `.matches("x")`,
	}
	for name, data := range map[string]map[string]any{"cyclic": cyclic, "dag": dag} {
		for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields(path))) {
			for _, expr := range exprs {
				start := time.Now()
				_, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
				require.ErrorIs(t, err, filter.ErrDataLimitExceeded, "%s %s: %s", name, mode, expr)
				requireQuick(t, time.Since(start), raceSlowdown*5*time.Second, "%s %s: %s", name, mode, expr)
			}
		}
	}

	// The same shapes, a few steps deep, are read in full.
	short := "a.a.a"
	for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields(short))) {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, "has("+short+")"), cyclic)
		require.NoError(t, err, mode)
		require.True(t, got, mode)
	}
}

// TestEvaluator_DataBudgetCharges: the data budget ([evaluator.WithMaxDataBudget]) counts the values read and compared, a typed slice
// copied by its length, a string a function scans by its length (a regex by the pattern's too) and a decimal comparison more than
// another; an evaluation beyond it fails with ErrDataLimitExceeded in both modes, and the same one within a larger budget succeeds.
func TestEvaluator_DataBudgetCharges(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 64*200)

	list := make([]any, 2000)
	for i := range list {
		list[i] = int64(i)
	}

	data := map[string]any{
		"long": long, "medium": strings.Repeat("y", 64*10), "list": list, "typed": make([]string, 200),
		"price": decimal.RequireFromString("1.5"),
	}

	cases := []struct {
		expr   string
		budget int // the smallest budget tried: it is exceeded; ten times as much is not
	}{
		{`long.contains("y")`, 100},
		{`long.startsWith("y")`, 100},
		{`long.matches("y")`, 100},
		{"size(long) == 1", 100},
		{`medium.matches("` + strings.Repeat("x", 160) + `")`, 100},
		{"list == 5000", 1000},
		{"5000 in list", 1000},
		{`typed == "x"`, 100},
		{"price in [1, 2, 3]", 100},
	}
	for _, tc := range cases {
		for mode, eval := range evaluators(t, filtereval.WithMaxDataBudget(tc.budget), withPolicy(filter.WithArrayFields("list", "typed"))) {
			_, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), data)
			require.ErrorIs(t, err, filter.ErrDataLimitExceeded, "%s: %s", mode, tc.expr)
		}

		for mode, eval := range evaluators(t, filtereval.WithMaxDataBudget(10*tc.budget), withPolicy(filter.WithArrayFields("list", "typed"))) {
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), data)
			require.NoError(t, err, "%s: %s", mode, tc.expr)
			require.False(t, got, "%s: %s", mode, tc.expr)
		}
	}

	// A comparison costs a unit whatever the length of the string: only the functions that scan it pay for its bytes.
	for mode, eval := range evaluators(t, filtereval.WithMaxDataBudget(10)) {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, `long == "x" || long < "y"`), data)
		require.NoError(t, err, mode)
		require.True(t, got, mode)
	}
	// The default evaluator compares two fields too, which a literal does not bound; it substrings a string by copying its runes.
	plain, err := newEvaluator(filtereval.WithMaxDataBudget(100))
	require.NoError(t, err)

	for _, expr := range []string{"long.substring(0, 1) == \"x\"", "long == long"} {
		_, err = plain.Evaluate(filtertest.MustParseFilter(t, expr), data)
		require.ErrorIs(t, err, filter.ErrDataLimitExceeded, expr)
	}

	// A long key costs its hashing at every step that reads it.
	key := strings.Repeat("k", 64*30)
	keyed := map[string]any{key: int64(1)}

	for budget, exceeded := range map[int]bool{20: true, 200: false} {
		for mode, eval := range evaluators(t, filtereval.WithMaxDataBudget(budget)) {
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, key+" == 1"), keyed)
			if exceeded {
				require.ErrorIs(t, err, filter.ErrDataLimitExceeded, mode)
				continue
			}

			require.NoError(t, err, mode)
			require.True(t, got, mode)
		}
	}
}

// TestEvaluator_OversizedValuesOpaque: the cases of review finding R12-2 — a typed array of 2^30 empty structs takes no memory,
// but its []any would take 16 GiB (verified on the unfixed code: the evaluation allocates it); a value whose encoding exceeds the
// 16 MiB a MongoDB document holds is not encoded. Both are opaque — they match nothing but != — in bounded time and memory, as a
// struct holding them is.
func TestEvaluator_OversizedValuesOpaque(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", 17<<20)

	values := map[string]any{
		"array of 2^30 empty structs":        [1 << 30]struct{}{},
		"struct holding it":                  struct{ A [1 << 30]struct{} }{},
		"typed slice beyond 2^20 elements":   make([]bool, 1<<20+1),
		"struct encoding beyond 16 MiB":      struct{ Name string }{Name: huge},
		"struct holding a slice beyond 2^20": struct{ L []bool }{L: make([]bool, 1<<20+1)},
	}
	for name, v := range values {
		data := map[string]any{"v": v}

		for mode, eval := range evaluators(t, withPolicy(filter.WithArrayFields("v.name"))) {
			for _, expr := range []string{`v.name == "x"`, "has(v.name)", "v == null", `v.name.startsWith("x")`, "v in [1, 2]"} {
				start := time.Now()
				got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
				require.NoError(t, err, "%s %s: %s", name, mode, expr)
				require.False(t, got, "%s %s: %s", name, mode, expr)
				requireQuick(t, time.Since(start), 5*time.Second, "%s %s: %s", name, mode, expr)
			}
		}
	}
}

// TestDecimalLiteral_LongStringBounded: a data string compared with a decimal is parsed in time quadratic in its length; one longer
// than any Decimal128 spelling is refused before parsing (verified on the unfixed code: seconds for four million digits).
func TestDecimalLiteral_LongStringBounded(t *testing.T) {
	t.Parallel()

	start := time.Now()
	_, err := filter.DecimalLiteral(strings.Repeat("7", 4<<20))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
	requireQuick(t, time.Since(start), time.Second)

	d, err := filter.DecimalLiteral("0." + strings.Repeat("0", 6000) + "1")
	require.NoError(t, err, "a Decimal128 spelled with its leading zeros")
	require.Equal(t, int32(-6001), d.Exponent())
}

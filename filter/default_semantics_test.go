// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// shapesRow is a document with every shape a field can take.
func shapesRow() map[string]any {
	return map[string]any{
		"name":    "ab",
		"empty":   "",
		"null":    nil,
		"age":     int64(30),
		"ratio":   1.5,
		"nan":     math.NaN(),
		"active":  true,
		"off":     false,
		"tags":    []any{"red", "green"},
		"nums":    []any{int64(3), int64(9)},
		"none":    []any{},
		"price":   decimal.RequireFromString("12.30"),
		"created": time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC),
		"address": map[string]any{"city": "NYC", "zip": nil},
		"steps": []any{
			map[string]any{"providerId": "a", "codes": []any{"x", "y"}},
			map[string]any{"providerId": "b"},
			map[string]any{"other": int64(1)},
		},
	}
}

func TestEvaluator_DefaultSemanticsUnchanged(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator()
	require.NoError(t, err)

	_, err = eval.Evaluate(filtertest.MustParseFilter(t, `active > false`), shapesRow())
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)

	// An ordering comparison and size() of a missing field are unknown, as in SQL: no match, also negated.
	for _, expr := range []string{`missing < 1`, `missing.size() == 0`, `!(missing < 1)`, `!(missing.size() == 0)`} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), shapesRow())
		require.NoError(t, err, expr)
		require.False(t, got, expr)
	}

	got, err := eval.Evaluate(filtertest.MustParseFilter(t, `note == ""`), map[string]any{})
	require.NoError(t, err)
	require.False(t, got, "default semantics do not replace missing fields with zero values")
	got, err = eval.Evaluate(filtertest.MustParseFilter(t, `tags.size() > -1`), shapesRow())
	require.NoError(t, err)
	require.True(t, got, "size() operands are only range-checked under WithProfile")
	got, err = eval.Evaluate(filtertest.MustParseFilter(t, `tags.size() > 2147483648`), shapesRow())
	require.NoError(t, err, "default evaluator semantics do not impose MongoDB's size() bound")
	require.False(t, got)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestWithArrayFields_Context(t *testing.T) {
	t.Parallel()

	fields := []string{"tags", "steps.providerId"}
	ctx, err := filter.NewPolicy(filter.WithArrayFields(fields...), filter.WithAnyField(true))
	require.NoError(t, err)

	fields[0] = "late" // the option copies the list

	require.True(t, ctx.IsArrayField("tags"))
	require.True(t, ctx.IsArrayField("steps.providerId"))
	require.False(t, ctx.IsArrayField("late"))

	bare, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	require.False(t, bare.IsArrayField("tags"))
}

func TestEvaluator_ArrayFields(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator(withPolicy(filter.WithArrayFields("tags", "scores", "missing", "steps.providerId")))
	require.NoError(t, err)

	data := map[string]any{
		"tags":   []any{"red", "green"},
		"scores": []any{int64(3), int64(9)},
		"steps": []any{
			map[string]any{"providerId": "a"},
			map[string]any{"providerId": "b"},
			map[string]any{"other": 1},
		},
	}

	tests := []struct {
		expr string
		want bool
	}{
		{`tags == "red"`, true},
		{`tags == "blue"`, false},
		{`tags != "red"`, false},
		{`tags != "blue"`, true},
		{`"green" in tags`, true},
		{`"blue" in tags`, false},
		{`tags in ["blue", "green"]`, true},
		{`tags in ["blue"]`, false},
		{`scores > 8`, true},
		{`scores < 3`, false},
		{`tags.startsWith("gr")`, true},
		{`tags.endsWith("x")`, false},
		{`tags.matches("^r.d$")`, true},
		{`tags.size() == 2`, true},
		{`missing.size() == 0`, true},
		{`size(missing) < 1`, true},
		{`steps.providerId == "b"`, true},
		{`steps.providerId.size() == 2`, true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), data)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	// Without the declaration a list compares as a whole, as before.
	plain, err := newEvaluator()
	require.NoError(t, err)
	_, err = plain.Evaluate(filtertest.MustParseFilter(t, `tags == "red"`), data)
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
	got, err := plain.Evaluate(filtertest.MustParseFilter(t, `tags.startsWith("r")`), data)
	require.NoError(t, err)
	require.False(t, got)

	// Element comparisons stay strict without WithProfile.
	_, err = eval.Evaluate(filtertest.MustParseFilter(t, `tags < 3`), data)
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
}

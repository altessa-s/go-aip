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

func TestParser_GlobalStringCallsTakeTheMethodForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		global, method string
		op             filter.Operator
	}{
		{`contains(name, "oh")`, `name.contains("oh")`, filter.OpContains},
		{`startsWith(name, "J")`, `name.startsWith("J")`, filter.OpStartsWith},
		{`endsWith(address.city, "rk")`, `address.city.endsWith("rk")`, filter.OpEndsWith},
	}
	for _, tt := range tests {
		t.Run(tt.global, func(t *testing.T) {
			t.Parallel()
			got := filtertest.MustParseFilter(t, tt.global)
			require.Equal(t, filtertest.MustParseFilter(t, tt.method), got)
			call, ok := got.(*filter.CallNode)
			require.True(t, ok)
			require.Equal(t, tt.op, call.Op)
			require.IsType(t, &filter.IdentNode{}, call.Target)
			require.Len(t, call.Args, 1)
		})
	}
}

func TestParser_OtherStringCallSpellingsAreKept(t *testing.T) {
	t.Parallel()

	// One argument and no target is left as written; the evaluator and translators reject it as malformed.
	call, ok := filtertest.MustParseFilter(t, `contains("x")`).(*filter.CallNode)
	require.True(t, ok)
	require.Nil(t, call.Target)
	require.Len(t, call.Args, 1)

	eval, err := newEvaluator()
	require.NoError(t, err)
	_, err = eval.Evaluate(call, map[string]any{})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestEvaluator_GlobalStringCalls(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator()
	require.NoError(t, err)

	data := map[string]any{"name": "John"}
	for expr, want := range map[string]bool{
		`contains(name, "oh")`:  true,
		`startsWith(name, "J")`: true,
		`endsWith(name, "J")`:   false,
		`!contains(name, "x")`:  true,
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

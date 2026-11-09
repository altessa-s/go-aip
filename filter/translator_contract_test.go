// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func TestUnderscoreFunctionBypassesAllowlist(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithAllowedFunctions("contains"), filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
		"_hidden": filterfuncs.Constant("secret", filter.OpEqual, true),
	}))
	require.NoError(t, err)
	_, err = p.Parse(t.Context(), `_hidden()`)
	require.ErrorIs(t, err, filter.ErrFunctionNotAllowed)
}

func TestEvaluatorTypedLiterals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, expr string
		data       map[string]any
	}{
		{"timestamp", `createdAt >= timestamp("2024-01-01T00:00:00Z")`, map[string]any{"createdAt": time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{"bytes", `blob == b"abc"`, map[string]any{"blob": []byte("abc")}},
		{"bytes order", `blob < b"abd"`, map[string]any{"blob": []byte("abc")}},
		{"timestamp membership across zones", `createdAt in [timestamp("2024-01-01T00:00:00Z")]`, map[string]any{"createdAt": time.Date(2024, 1, 1, 1, 0, 0, 0, time.FixedZone("offset", 3600))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e, err := newEvaluator()
			require.NoError(t, err)
			got, err := e.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.data)
			require.NoError(t, err)
			require.True(t, got)
		})
	}
}

func TestEvaluatorListDepthBudget(t *testing.T) {
	t.Parallel()

	e, err := newEvaluator(withPolicy(filter.WithMaxDepth(2)))
	require.NoError(t, err)
	got, err := e.Evaluate(filtertest.MustParseFilter(t, `[[[[[1]]]]].size() == 1`), nil)
	t.Logf("value=%v error=%v", got, err)
	require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
}

func TestEnumOverflowRejected(t *testing.T) {
	t.Parallel()

	e, err := newEvaluator(withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"state": filter.FieldKindInt})), withPolicy(filter.WithEnumValues(map[string][]int64{"state": {0, 1}})))
	require.NoError(t, err)
	_, err = e.Evaluate(filtertest.MustParseFilter(t, `state == 18446744073709551615u`), map[string]any{"state": uint64(18446744073709551615)})
	require.ErrorIs(t, err, filter.ErrEnumValueNotAllowed)
}

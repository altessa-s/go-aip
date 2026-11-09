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

// TestEvaluator_ArrayFieldThroughDocuments: a declared array field reached through a list of documents whose leaves are lists
// tests the elements of those lists in both evaluator modes, as the MongoDB query does.
func TestEvaluator_ArrayFieldThroughDocuments(t *testing.T) {
	t.Parallel()

	data := map[string]any{"steps": []any{map[string]any{"codes": []any{"red"}}}}

	opts := []any{withPolicy(filter.WithArrayFields("steps.codes"))}

	eval, err := newEvaluator(opts...)
	require.NoError(t, err)

	for expr, want := range map[string]bool{
		`steps.codes == "red"`: true, `"red" in steps.codes`: true, `steps.codes != "red"`: false,
		`steps.codes in ["red"]`: true, `steps.codes.startsWith("r")`: true, `steps.codes > "a"`: true,
		`steps.codes.size() == 1`: true, `has(steps.codes)`: true,
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
		require.NoError(t, err, "%s", expr)
		require.Equal(t, want, got, "%s", expr)
	}
}

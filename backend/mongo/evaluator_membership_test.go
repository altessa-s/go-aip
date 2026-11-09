// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// TestEvaluator_LiteralInArrayField: `x in field` on a field declared by WithArrayFields is the comparison field == x under the
// MongoDB profile, as without a profile: x is checked against the declared element kind, and an absent or null array contains nothing.
func TestEvaluator_LiteralInArrayField(t *testing.T) {
	t.Parallel()

	opts := []any{withPolicy(filter.WithArrayFields("tags")), withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"tags": filter.FieldKindString}))}
	eval, err := newEvaluator(append([]any{filtereval.WithProfile(mongo.DefaultSchema())}, opts...)...)
	require.NoError(t, err)

	for _, expr := range []string{`1 in tags`, `true in tags`, `!(1 in tags)`} {
		for _, data := range []map[string]any{{"tags": []any{"red"}}, {"tags": nil}, {}} {
			_, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
			require.ErrorIs(t, err, filter.ErrFieldTypeMismatch, "%s on %v", expr, data)
		}
	}

	tests := []struct {
		expr string
		data map[string]any
		want bool
	}{
		{`"red" in tags`, map[string]any{"tags": []any{"green", "red"}}, true},
		{`"blue" in tags`, map[string]any{"tags": []any{"green", "red"}}, false},
		{`"red" in tags`, map[string]any{"tags": []any{}}, false},
		{`"red" in tags`, map[string]any{"tags": nil}, false},
		{`"red" in tags`, map[string]any{}, false},
		{`!("red" in tags)`, map[string]any{"tags": nil}, true},
		{`!("red" in tags)`, map[string]any{}, true},
		{`null in tags`, map[string]any{"tags": []any{"red"}}, false},
	}
	for _, tt := range tests {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), tt.data)
		require.NoError(t, err, "%s on %v", tt.expr, tt.data)
		require.Equal(t, tt.want, got, "%s on %v", tt.expr, tt.data)

		equality := map[string]string{
			`"red" in tags`: `tags == "red"`, `"blue" in tags`: `tags == "blue"`, `!("red" in tags)`: `!(tags == "red")`,
			`null in tags`: `tags == null`,
		}[tt.expr]
		same, err := eval.Evaluate(filtertest.MustParseFilter(t, equality), tt.data)
		require.NoError(t, err)
		require.Equal(t, same, got, "%s agrees with %s on %v", tt.expr, equality, tt.data)
	}
}

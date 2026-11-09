// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func TestArrayPredicatesWithNullOperand(t *testing.T) {
	t.Parallel()

	for _, predicate := range []string{
		`tags == text`, `tags > text`, `tags >= text`, `tags < text`, `tags <= text`,
		`tags.contains(text)`, `tags.startsWith(text)`, `tags.endsWith(text)`,
	} {
		t.Run(predicate, func(t *testing.T) {
			t.Parallel()

			// A null value satisfies no comparison with the elements of an empty or NULL array, as in the default evaluator, and is
			// unknown for any other array. A string function with a NULL argument is unknown even on an empty array.
			comparison := !strings.Contains(predicate, ".")
			for _, tc := range []struct {
				name    string
				array   any
				negated bool
			}{
				{"empty", []string{}, comparison}, {"null array", nil, comparison},
				{"null element", []any{nil}, false}, {"nonempty", []string{"a"}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					row := map[string]any{"tags": tc.array, "text": nil}
					direct := compile(t, schema(t), predicate)
					matched, err := direct.Evaluate(row, postgres.DefaultMaxDataBudget, nil)
					require.NoError(t, err)
					require.False(t, matched)
					negated := compile(t, schema(t), "!("+predicate+")")
					matched, err = negated.Evaluate(row, postgres.DefaultMaxDataBudget, nil)
					require.NoError(t, err)
					require.Equal(t, tc.negated, matched)
				})
			}
		})
	}

	p := compile(t, schema(t), `tags != text`)
	matched, err := p.Evaluate(map[string]any{"tags": []string{}, "text": nil}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, matched)
}

func TestListCommonNumericType(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"i": sqlbase.Scalar("i", sqlbase.Integer), "f": sqlbase.Scalar("f", sqlbase.Float), "n": sqlbase.Scalar("n", sqlbase.Numeric),
		"nums": sqlbase.Array("nums", sqlbase.Numeric), "fs": sqlbase.Array("fs", sqlbase.Float),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`i in [f]`, map[string]any{"i": int64(1), "f": 1.1}, false},
		{`!(i in [f])`, map[string]any{"i": int64(1), "f": 1.1}, true},
		{`i in [f, null]`, map[string]any{"i": int64(1), "f": 1.1}, false},
		{`!(i in [f, null])`, map[string]any{"i": int64(1), "f": 1.1}, true},
		{`i in [1.1, 2]`, map[string]any{"i": int64(1)}, false},
		{`i in [2, 1.1]`, map[string]any{"i": int64(2)}, true},
		{`i in [n]`, map[string]any{"i": int64(1), "n": "1.1"}, false},
		{`n in [i, 1.00000000000000001]`, map[string]any{"i": int64(2), "n": "1.00000000000000001"}, true},
		{`n in [f]`, map[string]any{"n": "1.00000000000000001", "f": 1.0}, true},
		{`[n, 1.00000000000000001] == [n, n]`, map[string]any{"n": "1"}, false},
		{`null in [n, "1"]`, map[string]any{"n": "1"}, false},
		{`nums == fs`, map[string]any{"nums": []string{"1.00000000000000001"}, "fs": []float64{1}}, true},
		{`fs == nums`, map[string]any{"nums": []string{"1.00000000000000001"}, "fs": []float64{1}}, true},
		{`[i, n] == [i, f]`, map[string]any{"i": int64(2), "n": "1.00000000000000001", "f": 1.0}, true},
		{`[[i], [f]] == [[f], [f]]`, map[string]any{"i": int64(9007199254740993), "f": float64(9007199254740992)}, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			p := compile(t, s, tc.expr)
			got, err := p.Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestUnknownNullLists(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`null in [null]`, true}, {`!(null in [null])`, false},
		{`null in [[null]]`, true}, {`null in []`, false}, {`!(null in [])`, true},
		{`[null] == [null]`, true}, {`[[null]] == [[null]]`, true},
		{`null in [null] || true`, true}, {`!(null in [null] && false)`, true},
		{`[null, 1] == [null, 1]`, true},
		{`size([null]) == 1`, true}, {`size([[null]]) == 1`, true}, {`size([]) == 0`, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			p := compile(t, schema(t), tc.expr)
			got, err := p.Evaluate(nil, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			_, _, err = p.SQL()
			require.NoError(t, err)
			_, err = p.Inline()
			require.NoError(t, err)
		})
	}
}

func TestWholeArrayCastChecksAllElements(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"nums": sqlbase.Array("nums", sqlbase.Numeric), "fs": sqlbase.Array("fs", sqlbase.Float)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)

	for _, expr := range []string{`nums < fs`, `fs > nums`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			p := compile(t, s, expr)
			_, err := p.Evaluate(map[string]any{"nums": []string{"1", "1e1000"}, "fs": []float64{2, 3}}, postgres.DefaultMaxDataBudget, nil)
			require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
		})
	}
}

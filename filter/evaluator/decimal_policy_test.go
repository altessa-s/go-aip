// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestDecimalPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{`price == null`, false}, {`price != null`, true}, {`price == "one"`, true}, {`price in ["one"]`, true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			t.Parallel()

			e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithDecimalParser(func(n filter.Node, v any) (decimal.Decimal, error) {
				if v == "one" {
					return decimal.NewFromInt(1), nil
				}

				return filter.DecimalOperand(n, v)
			})))
			require.NoError(t, err)
			node := filtertest.MustParseFilter(t, tc.expression)
			data := map[string]any{"price": decimal.NewFromInt(1)}
			got, err := e.Evaluate(node, data)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			prepared, err := e.Prepare(node)
			require.NoError(t, err)
			got, err = prepared.Evaluate(data)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

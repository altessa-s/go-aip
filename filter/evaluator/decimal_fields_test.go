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

// decimalPolicy declares price and cost decimal fields and prices a decimal array field.
func decimalPolicy(t *testing.T) *filter.Policy {
	t.Helper()

	return filtertest.MustPolicy(t,
		filter.WithAllowedFields("price", "cost", "prices", "qty"),
		filter.WithArrayFields("prices"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"price": filter.FieldKindDecimal, "cost": filter.FieldKindDecimal, "prices": filter.FieldKindDecimal, "qty": filter.FieldKindFloat,
		}))
}

// TestDecimalFieldsCompareExactly: a field declared FieldKindDecimal compares exactly whatever Go form its data takes, as the SQL
// and MongoDB translators compare a decimal column.
func TestDecimalFieldsCompareExactly(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(decimalPolicy(t))
	require.NoError(t, err)

	forms := []any{1.5, "1.50", decimal.RequireFromString("1.5"), "15e-1"}

	tests := []struct {
		expr string
		want bool
	}{
		{`price == "1.5"`, true},
		{`price == 1.5`, true},
		{`price == 1.50000000000000001`, false},
		{`price != "1.5"`, false},
		{`price > 1`, true},
		{`price >= "1.5"`, true},
		{`price < 2`, true},
		{`price <= 1.4`, false},
		{`price in [1.5, "2"]`, true},
		{`price in ["2", 3]`, false},
		{`price == null`, false},
	}
	for _, form := range forms {
		for _, tt := range tests {
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), map[string]any{"price": form})
			require.NoError(t, err, "%s on %#v", tt.expr, form)
			require.Equal(t, tt.want, got, "%s on %#v", tt.expr, form)
		}
	}

	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`price == 2`, map[string]any{"price": int64(2)}, true},
		{`price == "2.00"`, map[string]any{"price": int32(2)}, true},
		{`price == "18446744073709551615"`, map[string]any{"price": uint64(math.MaxUint64)}, true},
		{`price == 0.3`, map[string]any{"price": 0.30000000000000004}, false},
		{`price == "0.30000000000000004"`, map[string]any{"price": 0.30000000000000004}, true},
		{`price >= "12345678901234567.89"`, map[string]any{"price": "12345678901234567.89"}, true},
		{`price > "10"`, map[string]any{"price": "9"}, false},
		{`price == null`, map[string]any{}, true},
		{`price == null`, map[string]any{"price": nil}, true},
		{`price == cost`, map[string]any{"price": "1.50", "cost": 1.5}, true},
		{`price < cost`, map[string]any{"price": "1.4", "cost": decimal.RequireFromString("1.5")}, true},
		{`price == qty`, map[string]any{"price": "1.5", "qty": 1.5}, true},
		{`prices == 1.5`, map[string]any{"prices": []any{"1.50", 2.0}}, true},
		{`prices > "1.9"`, map[string]any{"prices": []any{"1.50", 2.0}}, true},
		{`"2" in prices`, map[string]any{"prices": []any{"1.50", 2.0}}, true},
		{`prices in ["2.0"]`, map[string]any{"prices": []any{"1.50", 2.0}}, true},
		{`price == 1`, map[string]any{"price": math.NaN()}, false},
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.row)
		require.NoError(t, err, "%s on %v", tc.expr, tc.row)
		require.Equal(t, tc.want, got, "%s on %v", tc.expr, tc.row)
	}

	// A data value that is no decimal compares as the value it is.
	for _, row := range []map[string]any{{"price": "abc"}, {"price": true}} {
		_, err := eval.Evaluate(filtertest.MustParseFilter(t, `price == 1`), row)
		require.ErrorIs(t, err, filter.ErrUnsupportedType, "%v", row)
	}
}

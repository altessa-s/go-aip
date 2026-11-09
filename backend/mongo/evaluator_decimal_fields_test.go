// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
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

// TestDecimalFieldsMongoProfileKeepStoredForm: under WithProfile the data keeps its stored form, as MongoDB compares a stored string
// or double with the Decimal128 operand the translator emits.
func TestDecimalFieldsMongoProfileKeepStoredForm(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(decimalPolicy(t), evaluator.WithProfile(mongo.DefaultSchema()))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`price == "1.5"`, map[string]any{"price": "1.5"}, false},
		{`price == "1.5"`, map[string]any{"price": decimal.RequireFromString("1.50")}, true},
		{`price == 0.1`, map[string]any{"price": 0.1}, false},
		{`price > 1`, map[string]any{"price": 1.5}, true},
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.row)
		require.NoError(t, err, tc.expr)
		require.Equal(t, tc.want, got, "%s on %v", tc.expr, tc.row)
	}
}

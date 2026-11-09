// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"slices"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func TestDecimalKeysetPrecision(t *testing.T) {
	t.Parallel()

	type item struct {
		id   string
		cost decimal.Decimal
	}

	items := []item{{"a", decimal.NewFromInt(-1)}, {"b", decimal.Zero}, {"c", decimal.Zero}, {"d", decimal.RequireFromString("0.000000000001")}}
	schema := listing.MustSchema([]listing.Field{{Name: "cost", Kind: filter.FieldKindDecimal}}, []string{"cost"}, nil)
	lister, err := memory.NewListEngine(pagetokentest.Codec(t), schema, func(i item) string { return i.id }, func(i item) map[string]any { return map[string]any{"cost": i.cost} })
	require.NoError(t, err)

	for _, order := range []string{"cost", "cost desc"} {
		req := listing.Request{OrderBy: order, PageSize: 1}

		var got []string

		for {
			page, err := lister.List(t.Context(), "decimal", req, items)
			require.NoError(t, err)

			for _, i := range page.Items {
				got = append(got, i.id)
			}

			if page.NextPageToken == "" {
				break
			}

			require.LessOrEqual(t, len(got), len(items), "repeated cursor")

			req.PageToken = page.NextPageToken
		}

		want := []string{"a", "b", "c", "d"}
		if order == "cost desc" {
			slices.Reverse(want)
		}

		require.Equal(t, want, got, order)
	}
}

func TestDecimalPolicy(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"", `cost == "one"`, `cost in ["one"]`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			schema, err := listing.NewSchema([]listing.Field{{Name: "cost", Kind: filter.FieldKindDecimal}}, []string{"cost"}, listing.NewProfile(
				listing.WithDefaultEqualities(map[string]any{"cost": "1"}),
				listing.WithFilterOptions(filter.WithDecimalParser(func(node filter.Node, value any) (decimal.Decimal, error) {
					if value == "one" {
						return decimal.NewFromInt(1), nil
					}

					return filter.DecimalOperand(node, value)
				})),
			))
			require.NoError(t, err)
			lister, err := memory.NewListEngine(pagetokentest.Codec(t), schema, func(i int) string { return "item" }, func(i int) map[string]any { return map[string]any{"cost": decimal.NewFromInt(int64(i))} })
			require.NoError(t, err)
			page, err := lister.List(t.Context(), "decimal", listing.Request{Filter: expr}, []int{1, 2})
			require.NoError(t, err)
			require.Equal(t, []int{1}, page.Items)
			query, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
			require.NoError(t, err)
			storage, err := sqlbase.NewSchema(map[string]sqlbase.Field{"cost": sqlbase.Scalar("cost", sqlbase.Numeric)})
			require.NoError(t, err)
			contractSchema, err := mariadb.NewSchema(storage, mariadb.WithSchemaContractMode(true))
			require.NoError(t, err)
			translator, err := mariadb.NewFilterTranslatorWithSchema(schema.FilterPolicy(), contractSchema)
			require.NoError(t, err)
			_, args, err := translator.Translate(query.Filter)
			require.NoError(t, err)
			require.Equal(t, []any{"1"}, args)
		})
	}
}

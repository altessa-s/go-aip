// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestDecimalEncoderExactOperands(t *testing.T) {
	t.Parallel()

	encode := func(v any) (any, error) {
		d, ok := v.(decimal.Decimal)
		require.True(t, ok, "%T", v)

		return d.String(), nil
	}
	opts := []filter.PolicyOption{filter.WithAllowedFields("price"), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})}
	pg, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, opts...), postgres.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)
	ma, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, opts...), mariadb.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)
	ch, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, opts...), clickhouse.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)

	for _, tr := range []interface {
		Translate(filter.Node) (string, []any, error)
		TranslateInline(filter.Node) (string, error)
	}{pg, ma, ch} {
		for _, expr := range []string{"price == 1.00000000000000001", "price in [1.00000000000000001]", "1.00000000000000001 == price"} {
			n := filtertest.MustParseFilter(t, expr)
			_, args, err := tr.Translate(n)
			require.NoError(t, err)
			require.Equal(t, []any{"1.00000000000000001"}, args)

			sql, err := tr.TranslateInline(n)
			require.NoError(t, err)
			require.Contains(t, sql, "1.00000000000000001")
		}
	}
}

func TestTextDecimalEncodersReceiveExactOperands(t *testing.T) {
	t.Parallel()

	encode := func(v any) (any, error) {
		d, ok := v.(decimal.Decimal)
		require.True(t, ok, "encoder received %T", v)
		require.Equal(t, "1.00000000000000001", d.String())

		return d.String(), nil
	}
	opts := []filter.PolicyOption{filter.WithAllowedFields("price"), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})}
	me, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, opts...), meili.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)
	lu, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, opts...), lua.WithFilterTableVar("row"), lua.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)
	re, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), redisSchema(map[string]redis.FieldType{"price": redis.FieldTypeTag}), redis.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)

	for _, tr := range []interface {
		Translate(filter.Node) (string, error)
	}{me, lu, re} {
		for _, expr := range []string{"price == 1.00000000000000001", "price in [1.00000000000000001]"} {
			node := filtertest.MustParseFilter(t, expr)
			_, err := tr.Translate(node)
			require.NoError(t, err)
		}

		_, err := tr.Translate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "price"}, Right: &filter.LiteralNode{Value: decimal.RequireFromString("1.00000000000000001")}})
		require.NoError(t, err)
	}
}

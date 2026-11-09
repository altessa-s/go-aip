// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func TestClickHouseCheckSchemaAndIndexes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, typ           string
		kind                filter.FieldKind
		array, presence     bool
		scope, sort, bad    bool
		check               sqlbase.ListSchemaCheck
		precision           time.Duration
		decimal             sqlbase.DecimalFormat
		missing, projection bool
	}{
		{name: "string", typ: "String", kind: filter.FieldKindString, sort: true},
		{name: "low cardinality", typ: "LowCardinality(String)", kind: filter.FieldKindString},
		{name: "fixed string", typ: "FixedString(8)", kind: filter.FieldKindString, bad: true, check: sqlbase.CheckType},
		{name: "int64", typ: "Int64", kind: filter.FieldKindInt},
		{name: "unsigned", typ: "UInt64", kind: filter.FieldKindInt, bad: true, check: sqlbase.CheckType},
		{name: "bool", typ: "Bool", kind: filter.FieldKindBool},
		{name: "float", typ: "Float64", kind: filter.FieldKindFloat},
		{name: "bytes", typ: "String", kind: filter.FieldKindBytes},
		{name: "datetime64", typ: "DateTime64(3, 'UTC')", kind: filter.FieldKindTimestamp},
		{name: "datetime", typ: "DateTime", kind: filter.FieldKindTimestamp},
		{name: "datetime64 finer", typ: "DateTime64(6)", kind: filter.FieldKindTimestamp, bad: true, check: sqlbase.CheckTimestampPrecision},
		{name: "datetime64 micros", typ: "DateTime64(6)", kind: filter.FieldKindTimestamp, precision: time.Microsecond},
		{name: "datetime64 garbage", typ: "DateTime64(x)", kind: filter.FieldKindTimestamp, bad: true, check: sqlbase.CheckTimestampPrecision},
		{name: "decimal", typ: "Decimal(18, 2)", kind: filter.FieldKindDecimal},
		{name: "decimal128", typ: "Decimal128(30)", kind: filter.FieldKindDecimal},
		{name: "decimal256", typ: "Decimal256(30)", kind: filter.FieldKindDecimal, bad: true, check: sqlbase.CheckDecimalDomain},
		{name: "decimal scale", typ: "Decimal(38, 4)", kind: filter.FieldKindDecimal, decimal: sqlbase.DecimalFormat{Precision: 38, Scale: 2}, bad: true,
			check: sqlbase.CheckDecimalDomain},
		{name: "decimal garbage", typ: "Decimal(x)", kind: filter.FieldKindDecimal, bad: true, check: sqlbase.CheckDecimalDomain},
		{name: "array", typ: "Array(String)", kind: filter.FieldKindString, array: true},
		{name: "array low cardinality", typ: "Array(LowCardinality(String))", kind: filter.FieldKindString, array: true},
		{name: "array of decimal", typ: "Array(Decimal(18, 2))", kind: filter.FieldKindDecimal, array: true},
		{name: "array null elements", typ: "Array(Nullable(String))", kind: filter.FieldKindString, array: true, bad: true, check: sqlbase.CheckNullability},
		{name: "array as scalar", typ: "Array(String)", kind: filter.FieldKindString, bad: true, check: sqlbase.CheckType},
		{name: "nested array", typ: "Array(Array(String))", kind: filter.FieldKindString, array: true, bad: true, check: sqlbase.CheckType},
		{name: "presence", typ: "Nullable(Int64)", kind: filter.FieldKindInt, presence: true},
		{name: "presence low cardinality", typ: "LowCardinality(Nullable(String))", kind: filter.FieldKindString, presence: true},
		{name: "presence not null", typ: "Int64", kind: filter.FieldKindInt, presence: true, bad: true, check: sqlbase.CheckNullability},
		{name: "nullable required", typ: "Nullable(Int64)", kind: filter.FieldKindInt, bad: true, check: sqlbase.CheckNullability},
		{name: "scope", typ: "String", scope: true},
		{name: "nullable scope", typ: "Nullable(String)", scope: true, bad: true, check: sqlbase.CheckNullability},
		{name: "array scope", typ: "Array(String)", scope: true, bad: true, check: sqlbase.CheckNullability},
		{name: "missing", typ: "String", kind: filter.FieldKindString, missing: true, bad: true, check: sqlbase.CheckColumn},
		{name: "missing scope", typ: "String", scope: true, missing: true, bad: true, check: sqlbase.CheckColumn},
		{name: "missing projection", typ: "String", kind: filter.FieldKindString, projection: true, bad: true, check: sqlbase.CheckColumn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
				if strings.HasPrefix(query, "ALTER TABLE") {
					return sqlbasetest.Reply{}
				}

				rows := [][]driver.Value{{"id", "String"}}
				if !tc.missing {
					rows = append(rows, []driver.Value{"value", tc.typ})
				}

				return sqlbasetest.Reply{Columns: []string{"name", "type"}, Rows: rows}
			})

			fields := []listing.Field{{Name: "id", Kind: filter.FieldKindString}}

			var opts []sqlbase.ListEngineOption
			if tc.scope {
				opts = append(opts, sqlbase.WithListScopeKeys("value"), sqlbase.WithListParentKey("value"))
			} else {
				fields = append(fields, listing.Field{Name: "value", Kind: tc.kind, Array: tc.array, Presence: tc.presence})
			}

			if tc.precision != 0 {
				opts = append(opts, sqlbase.WithListTimestampPrecision(tc.precision))
			}

			if tc.decimal != (sqlbase.DecimalFormat{}) {
				opts = append(opts, sqlbase.WithListDecimalFormat(tc.decimal))
			}

			orders := []string{"id"}
			if tc.sort {
				orders = []string{"value"}
			}

			projection := []string{"id"}
			if tc.projection {
				projection = []string{"absent"}
			}

			var failed []sqlbase.ListSchemaCheck

			opts = append(opts, sqlbase.WithListSchemaCheckPolicy(func(_ context.Context, _ *sql.DB, result sqlbase.ListSchemaCheckResult) error {
				if result.Err != nil {
					failed = append(failed, result.Check)
				}

				return result.Err
			}))

			schema, err := listing.NewSchema(fields, orders, nil)
			require.NoError(t, err)
			l, err := clickhouse.NewListEngine[struct{ ID string }](db, nil, schema, "db.widgets", projection,
				func(r *struct{ ID string }) []any { return []any{&r.ID} }, opts...)
			require.NoError(t, err)

			err = l.CheckSchema(t.Context(), db)
			require.Equal(t, []any{"db", "db", "widgets"}, fake.Calls()[0].Args)
			require.Contains(t, fake.Calls()[0].Query, "FROM system.columns")

			if tc.bad {
				require.ErrorIs(t, err, listing.ErrInvalidSchema)
				require.Equal(t, tc.check, failed[0])
				require.ErrorIs(t, l.EnsureIndexes(t.Context(), db), listing.ErrInvalidSchema)

				return
			}

			require.NoError(t, err)
			require.NoError(t, l.EnsureIndexes(t.Context(), db))

			indexes := l.Indexes()
			require.NotEmpty(t, indexes)

			for _, ddl := range indexes {
				require.True(t, strings.HasPrefix(ddl, "ALTER TABLE `db`.`widgets` ADD INDEX IF NOT EXISTS `widgets_"), ddl)
				require.True(t, strings.HasSuffix(ddl, ") TYPE minmax GRANULARITY 1"), ddl)
				require.NotContains(t, ddl, " ASC")
				require.NotContains(t, ddl, " DESC")
			}

			calls := fake.Calls()
			require.Equal(t, indexes[len(indexes)-1], calls[len(calls)-1].Query)
		})
	}
}

func TestClickHouseIndexDDL(t *testing.T) {
	t.Parallel()

	d := clickhouse.ListDialect{}
	require.Equal(t, "ALTER TABLE `t` ADD INDEX IF NOT EXISTS `i` (`tenant`, `name`, `id`) TYPE minmax GRANULARITY 1",
		d.IndexDDL("`t`", "`i`", []string{"`tenant` ASC", "`name` DESC", "`id` ASC"}))
	require.Equal(t, "widgets_00_listing_idx", d.IndexName("db.widgets", 0))
	require.Equal(t, "x LIMIT 3", d.LimitSelect("x", 3))
	require.Equal(t, "a = ? AND b = ?", d.Bind("a = ? AND b = ?", 4))
	require.True(t, d.ValidIdentifier("created_at"))
	require.False(t, d.ValidIdentifier("created-at"))
	require.Equal(t, "toString(`id`)", d.IdentityExpression("`id`", sqlbase.Numeric))
	require.Equal(t, "`id`", d.IdentityExpression("`id`", sqlbase.Text))
	require.Equal(t, "toString(`v`)", d.SortKeyExpression("`v`", sqlbase.Numeric))

	table, err := d.Table("db.widgets")
	require.NoError(t, err)
	require.Equal(t, "`db`.`widgets`", table)

	_, err = d.Table("db.wid`gets")
	require.Error(t, err)

	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Err: errors.New("boom")} })
	require.Error(t, d.EnsureIndexes(t.Context(), db, "t", []string{"ALTER TABLE t"}))
	require.Error(t, d.CheckSchema(t.Context(), db, sqlbase.ListSchemaConfig{}))
}

func TestClickHouseCursorPredicate(t *testing.T) {
	t.Parallel()

	d := clickhouse.ListDialect{}
	keys := []sqlbase.ListOrderKey{
		{Column: "price", StorageKind: sqlbase.Numeric, Field: listing.Field{Kind: filter.FieldKindDecimal}},
		{Column: "id", StorageKind: sqlbase.Integer, Field: listing.Field{Kind: filter.FieldKindInt}, Desc: true},
	}

	price, err := pagetoken.NewSortValue(decimal.RequireFromString("1.5"))
	require.NoError(t, err)
	id, err := pagetoken.NewSortValue(int64(7))
	require.NoError(t, err)

	sql, args, err := d.CursorPredicate(keys, pagetoken.Position{Keys: []pagetoken.SortValue{price}, ID: id}, "_l", sqlbase.IDNative,
		sqlbase.DecimalFormat{Precision: 18, Scale: 2})
	require.NoError(t, err)
	require.Equal(t, "((`_l`.`price` > CAST(? AS Decimal(18, 2)) OR (`_l`.`price` = CAST(? AS Decimal(18, 2)) AND `_l`.`id` < ?)))", sql)
	require.Equal(t, []any{"1.5", "1.5", int64(7)}, args)

	_, _, err = d.CursorPredicate(keys, pagetoken.Position{ID: id}, "_l", sqlbase.IDNative, sqlbase.DefaultDecimalFormat())
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)

	_, _, err = d.CursorPredicate(keys, pagetoken.Position{Keys: []pagetoken.SortValue{price}, ID: id}, "_l", sqlbase.IDNative,
		sqlbase.DecimalFormat{Precision: 2, Scale: 0})
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "a cursor decimal outside the domain is an invalid token")

	text, err := pagetoken.NewSortValue("x")
	require.NoError(t, err)
	_, _, err = d.CursorPredicate(keys, pagetoken.Position{Keys: []pagetoken.SortValue{text}, ID: id}, "_l", sqlbase.IDNative, sqlbase.DefaultDecimalFormat())
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

func TestClickHouseDecimalIdentity(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"1.50", "1.50"}, {"2", "2"}}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindDecimal}}, []string{"id"}, nil)
	require.NoError(t, err)

	type item struct{ ID string }

	l, err := clickhouse.NewListEngine[item](db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, func(r *item) []any { return []any{&r.ID} },
		sqlbase.WithListIDKind(filter.FieldKindDecimal))
	require.NoError(t, err)

	page, err := l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextPageToken)
	require.Contains(t, fake.Calls()[0].Query, "SELECT toString(`id`), `id` FROM `widgets`")

	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, PageToken: page.NextPageToken}, nil)
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[1].Query, "(`_listing`.`id` > CAST(? AS Decimal(65, 30)))")
	require.Equal(t, []any{"1.5"}, fake.Calls()[1].Args)
}

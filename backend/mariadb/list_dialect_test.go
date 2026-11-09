// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestListDialectCursorPredicate(t *testing.T) {
	t.Parallel()

	sv := func(v any) pagetoken.SortValue {
		s, err := pagetoken.NewSortValue(v)
		require.NoError(t, err)

		return s
	}
	keys := []sqlbase.ListOrderKey{{Column: "rank", StorageKind: sqlbase.Integer, Desc: true}, {Column: "price", StorageKind: sqlbase.Numeric}}
	pos := pagetoken.Position{Keys: []pagetoken.SortValue{sv(int64(3))}, ID: sv(decimal.RequireFromString("1.25"))}
	d := mariadb.ListDialect{}

	query, args, err := d.CursorPredicate(keys, pos, "al", sqlbase.IDNative, sqlbase.DecimalFormat{Precision: 5, Scale: 2})
	require.NoError(t, err)
	require.Equal(t, "((`al`.`rank` < ? OR (`al`.`rank` = ? AND `al`.`price` > CAST(? AS DECIMAL(5,2)))))", query)
	require.Equal(t, []any{int64(3), int64(3), decimal.RequireFromString("1.25")}, args)

	_, _, err = d.CursorPredicate(keys, pos, "al", sqlbase.IDNative, sqlbase.DecimalFormat{Precision: 2, Scale: 1})
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "a cursor decimal outside the domain is an invalid token")

	_, _, err = d.CursorPredicate(keys, pagetoken.Position{ID: pos.ID}, "al", sqlbase.IDNative, sqlbase.DefaultDecimalFormat())
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

func TestListDialectSyntax(t *testing.T) {
	t.Parallel()

	d := mariadb.ListDialect{}
	require.Equal(t, "`a``b`", d.Quote("a`b"))
	require.Equal(t, "x LIMIT 3", d.LimitSelect("x", 3))
	require.Equal(t, "a = ? AND b = ?", d.Bind("a = ? AND b = ?", 4))
	require.Equal(t, "CAST(`id` AS CHAR)", d.IdentityExpression("`id`", sqlbase.Text))
	require.Equal(t, "`id`", d.IdentityExpression("`id`", sqlbase.Integer))
	require.Equal(t, "widgets_01_listing_idx", d.IndexName("db.widgets", 1))

	arrays := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}},
		[]string{"id"}, nil)
	require.NoError(t, d.Validate(arrays, sqlbase.ListStorageConfig{ArrayStorage: sqlbase.ArraysJSON}))
	require.ErrorIs(t, d.Validate(arrays, sqlbase.ListStorageConfig{}), listing.ErrInvalidSchema)
	require.ErrorIs(t, d.Validate(arrays, sqlbase.ListStorageConfig{ArrayStorage: 7}), listing.ErrInvalidSchema)
	require.ErrorIs(t, d.Validate(arrays, sqlbase.ListStorageConfig{IDType: sqlbase.IDUUID, ArrayStorage: sqlbase.ArraysJSON}),
		listing.ErrInvalidSchema)

	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Err: errors.New("boom")} })
	require.Error(t, d.EnsureIndexes(t.Context(), db, "t", []string{"CREATE INDEX i ON t (a)"}))
	require.Error(t, d.CheckSchema(t.Context(), db, sqlbase.ListSchemaConfig{Table: "t"}))
}

func TestListDialectCheckSchema(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "age", Kind: filter.FieldKindInt, Presence: true},
		{Name: "price", Kind: filter.FieldKindDecimal},
		{Name: "at", Kind: filter.FieldKindTimestamp},
		{Name: "tenant", Kind: filter.FieldKindString},
		{Name: "flag", Kind: filter.FieldKindBool},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"name", "flag desc", "price"}, nil)
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"}, Rows: [][]driver.Value{
			{"id", "varchar", "varchar(64)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil},
			{"name", "text", "text", "latin1", "x", "NO", nil},
			{"age", "bigint", "bigint(20) unsigned", nil, nil, "NO", nil},
			{"price", "decimal", "decimal(80,40)", nil, nil, "NO", nil},
			{"at", "datetime", "datetime(6)", nil, nil, "NO", int64(6)},
			{"tenant", "text", "text", "utf8mb4", "q", "YES", nil},
			{"flag", "int", "int", nil, nil, "NO", nil},
			{"tags", "varchar", "varchar(10)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil},
		}}
	})

	var results []string

	err := mariadb.ListDialect{}.CheckSchema(t.Context(), db, sqlbase.ListSchemaConfig{
		Schema: schema, Table: "widgets", Projection: []string{"col1"}, ScopeKeys: []string{"tenant"}, IDColumn: "id",
		IDKind: filter.FieldKindString, TimestampPrecision: time.Millisecond,
		Storage: sqlbase.ListStorageConfig{ArrayStorage: sqlbase.ArraysJSON, DecimalFormat: sqlbase.DefaultDecimalFormat()},
		CheckPolicy: func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
			if r.Err != nil {
				results = append(results, string(r.Check)+" "+r.Err.Error())
			}

			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		`column missing projection column "col1": listing: invalid schema`,
		`nullability scope column "tenant" must be NOT NULL: listing: invalid schema`,
		`collation scope column "tenant" must use utf8mb4 and collation "utf8mb4_nopad_bin": listing: invalid schema`,
		`signed_integer column "age" must use signed BIGINT: listing: invalid schema`,
		`nullability column "age" nullability does not match presence declaration: listing: invalid schema`,
		`timestamp_precision column "at" must align to timestamp precision 1ms: listing: invalid schema`,
		`type column "flag" has type "int", expected "tinyint": listing: invalid schema`,
		`collation column "name" must use utf8mb4 and collation "utf8mb4_nopad_bin": listing: invalid schema`,
		`decimal_domain column "price" exceeds configured decimal domain: listing: invalid schema`,
		`type array column "tags" must use JSON: listing: invalid schema`,
		`collation column "tenant" must use utf8mb4 and collation "utf8mb4_nopad_bin": listing: invalid schema`,
		`indexable indexed column "name" must use VARCHAR or CHAR, not TEXT: listing: invalid schema`,
		`indexable indexed column "tenant" must use VARCHAR or CHAR, not TEXT: listing: invalid schema`,
	}, results)
}

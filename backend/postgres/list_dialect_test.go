// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
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
	keys := func(desc ...bool) []sqlbase.ListOrderKey {
		kinds := []sqlbase.Kind{sqlbase.Text, sqlbase.Integer, sqlbase.Numeric}
		out := make([]sqlbase.ListOrderKey, len(desc))

		for i, d := range desc {
			out[i] = sqlbase.ListOrderKey{Column: fmt.Sprintf("c%d", i), StorageKind: kinds[i], Desc: d}
		}

		return out
	}
	two := pagetoken.Position{Keys: []pagetoken.SortValue{sv("x")}, ID: sv(int64(3))}
	three := pagetoken.Position{Keys: []pagetoken.SortValue{sv("x"), sv(int64(3))}, ID: sv(decimal.RequireFromString("1.25"))}

	for _, tc := range []struct {
		name  string
		keys  []sqlbase.ListOrderKey
		pos   pagetoken.Position
		id    sqlbase.IDType
		query string
		args  []any
	}{
		{name: "same direction", keys: keys(false, false), pos: two, query: `("al"."c0", "al"."c1") > (?, ?)`, args: []any{"x", int64(3)}},
		{name: "same direction descending", keys: keys(true, true), pos: two, query: `("al"."c0", "al"."c1") < (?, ?)`, args: []any{"x", int64(3)}},
		{name: "mixed directions", keys: keys(true, false), pos: two, query: `(("al"."c0" < ? OR ("al"."c0" = ? AND ("al"."c1" > ?))))`,
			args: []any{"x", "x", int64(3)}},
		{name: "uuid identity", keys: keys(false, true, false), pos: three, id: sqlbase.IDUUID,
			query: `(("al"."c0" > ? OR ("al"."c0" = ? AND (("al"."c1" < ? OR ("al"."c1" = ? AND ("al"."c2" > ?::uuid)))))))`,
			args:  []any{"x", "x", int64(3), int64(3), decimal.RequireFromString("1.25")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			query, args, err := postgres.ListDialect{}.CursorPredicate(tc.keys, tc.pos, "al", tc.id, sqlbase.DefaultDecimalFormat())
			require.NoError(t, err)
			require.Equal(t, tc.query, query)
			require.Equal(t, tc.args, args)
		})
	}

	_, _, err := postgres.ListDialect{}.CursorPredicate(keys(false, false), pagetoken.Position{ID: sv(int64(3))}, "al", sqlbase.IDNative,
		sqlbase.DefaultDecimalFormat())
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

func TestListDialectSyntax(t *testing.T) {
	t.Parallel()

	d := postgres.ListDialect{}
	require.Equal(t, `"a""b"`, d.Quote(`a"b`))
	require.Equal(t, "x LIMIT 3", d.LimitSelect("x", 3))
	require.Equal(t, "a = $4 AND b = $5", d.Bind("a = ? AND b = ?", 4))
	require.Equal(t, `"id"::text`, d.IdentityExpression(`"id"`, sqlbase.UUID))
	require.Equal(t, `"id"`, d.IdentityExpression(`"id"`, sqlbase.Integer))
	require.Equal(t, "widgets_03_listing_idx", d.IndexName("app.widgets", 3))
	require.Equal(t, `CREATE INDEX IF NOT EXISTS i ON t (a ASC, b DESC)`, d.IndexDDL("t", "i", []string{"a ASC", "b DESC"}))
	require.True(t, d.ValidIdentifier("created_at"))
	require.False(t, d.ValidIdentifier("created-at"))

	table, err := d.Table("app.widgets")
	require.NoError(t, err)
	require.Equal(t, `"app"."widgets"`, table)

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, d.Validate(schema, sqlbase.ListStorageConfig{IDType: sqlbase.IDUUID, ArrayStorage: sqlbase.ArraysJSON}))
	require.ErrorIs(t, d.Validate(schema, sqlbase.ListStorageConfig{ArrayStorage: 7}), listing.ErrInvalidSchema)

	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Err: errors.New("boom")} })
	require.Error(t, d.EnsureIndexes(t.Context(), db, "t", []string{"CREATE INDEX i ON t (a)"}))
	require.Error(t, d.CheckSchema(t.Context(), db, sqlbase.ListSchemaConfig{Table: "t"}))
}

func TestListDialectCheckSchema(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "at", Kind: filter.FieldKindTimestamp},
		{Name: "tenant", Kind: filter.FieldKindString},
	}, []string{"name"}, nil)
	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"n", "d", "u", "c", "nullable", "p"}, Rows: [][]driver.Value{
			{"id", "uuid", "uuid", nil, "NO", nil},
			{"name", "character varying", "varchar", "x", "YES", nil},
			{"at", "timestamp with time zone", "timestamptz", nil, "NO", int64(6)},
			{"tenant", "text", "text", "y", "YES", nil},
		}}
	})

	var results []string

	err := postgres.ListDialect{}.CheckSchema(t.Context(), db, sqlbase.ListSchemaConfig{
		Schema: schema, Table: "app.widgets", ScopeKeys: []string{"tenant"}, IDColumn: "id", IDKind: filter.FieldKindString,
		Storage: sqlbase.ListStorageConfig{IDType: sqlbase.IDUUID}, TimestampPrecision: time.Millisecond,
		CheckPolicy: func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
			if r.Err != nil {
				results = append(results, r.Err.Error())
			}

			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, []any{"app", "widgets"}, fake.Calls()[0].Args)
	require.Equal(t, []string{
		`scope column "tenant" must be NOT NULL: listing: invalid schema`,
		`scope column "tenant" must use collation "C": listing: invalid schema`,
		`column "at" must align to timestamp precision 1ms: listing: invalid schema`,
		`column "name" has type "varchar", expected "text": listing: invalid schema`,
		`column "name" nullability does not match presence declaration: listing: invalid schema`,
		`column "name" must use collation "C": listing: invalid schema`,
		`column "tenant" must use collation "C": listing: invalid schema`,
	}, results)
}

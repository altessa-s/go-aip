// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestListSortsNullableKeys pages on a Presence key: NULL sorts first ascending and last descending, its position holds
// the zero SortValue and the continuation predicate tests it with IS NULL.
func TestListSortsNullableKeys(t *testing.T) {
	t.Parallel()

	var rows [][]driver.Value

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: make([]string, len(rows[0])), Rows: rows}
	})
	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), ownerSchema(t, "owner"), "widgets", []string{"id"},
		func(id *string) []any { return []any{id} }, sqlbase.WithListUUIDFields("owner"))
	require.NoError(t, err)
	require.Equal(t, []string{`CREATE INDEX IF NOT EXISTS "widgets_00_listing_idx" ON "widgets" ("owner" ASC NULLS FIRST, "id" ASC)`}, l.Indexes())

	for _, tc := range []struct {
		order, orderSQL, next string
		rows                  [][]driver.Value
		args                  []any
	}{
		{
			order: "owner", rows: [][]driver.Value{{"a", nil, "a"}, {"b", nil, "b"}},
			orderSQL: `ORDER BY "_listing"."owner" ASC NULLS FIRST, "_listing"."id" ASC`,
			next:     `(("_listing"."owner" IS NOT NULL OR ("_listing"."owner" IS NULL AND "_listing"."id" > $1)))`,
			args:     []any{"a"},
		},
		{
			order: "owner desc", rows: [][]driver.Value{{"a", nil, "a"}, {"b", nil, "b"}},
			orderSQL: `ORDER BY "_listing"."owner" DESC NULLS LAST, "_listing"."id" DESC`,
			next:     `((("_listing"."owner" IS NULL AND "_listing"."id" < $1)))`,
			args:     []any{"a"},
		},
		{
			order: "owner desc", rows: [][]driver.Value{{"c", ownerUUID, "c"}, {"d", nil, "d"}},
			orderSQL: `ORDER BY "_listing"."owner" DESC NULLS LAST, "_listing"."id" DESC`,
			next: `(("_listing"."owner" < $1::uuid OR "_listing"."owner" IS NULL OR ("_listing"."owner" = $2::uuid AND ` +
				`"_listing"."id" < $3)))`,
			args: []any{ownerUUID, ownerUUID, "c"},
		},
		{
			order: "owner, age desc", rows: [][]driver.Value{{"c", ownerUUID, int64(3), "c"}, {"d", nil, int64(2), "d"}},
			orderSQL: `ORDER BY "_listing"."owner" ASC NULLS FIRST, "_listing"."age" DESC, "_listing"."id" DESC`,
			next: `(("_listing"."owner" > $1::uuid OR ("_listing"."owner" = $2::uuid AND ("_listing"."age" < $3 OR ` +
				`("_listing"."age" = $4 AND "_listing"."id" < $5)))))`,
			args: []any{ownerUUID, ownerUUID, int64(3), int64(3), "c"},
		},
	} {
		rows = tc.rows
		calls := len(fake.Calls())

		first, err := l.List(t.Context(), "widgets", listing.Request{PageSize: 1, OrderBy: tc.order}, nil)
		require.NoError(t, err, tc.order)
		require.Len(t, first.Items, 1)
		require.NotEmpty(t, first.NextPageToken)
		require.Contains(t, fake.Calls()[calls].Query, `"owner"::text`, "a UUID sort key is selected as text")
		require.Contains(t, fake.Calls()[calls].Query, tc.orderSQL)

		_, err = l.List(t.Context(), "widgets", listing.Request{PageSize: 1, OrderBy: tc.order, PageToken: first.NextPageToken}, nil)
		require.NoError(t, err, tc.order)

		next := fake.Calls()[calls+1]
		require.Contains(t, next.Query, "WHERE ("+tc.next+") "+tc.orderSQL, tc.order)
		require.Equal(t, tc.args, next.Args, tc.order)
	}
}

// TestNullableSortRequiresDialect refuses a Presence sort key on a dialect without ListNullableSortDialect, at
// construction for a declared order and per request for an unrestricted one.
func TestNullableSortRequiresDialect(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)
	dest := func(id *string) []any { return []any{id} }

	_, err := sqlbase.NewListEngine(db, pagetokentest.Codec(t), ownerSchema(t, "owner"), "widgets", []string{"id"}, dest, mariadb.ListDialect{})
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)

	_, err = clickhouse.NewListEngine(db, pagetokentest.Codec(t), ownerSchema(t, "owner desc"), "widgets", []string{"id"}, dest)
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, "ClickHouse")

	l, err := sqlbase.NewListEngine(db, pagetokentest.Codec(t), ownerSchema(t, "age"), "widgets", []string{"id"}, dest, mariadb.ListDialect{})
	require.NoError(t, err)

	_, err = l.List(t.Context(), "widgets", listing.Request{OrderBy: "owner desc"}, nil)
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)
	require.Equal(t, listing.FieldOrderBy, listingtest.FieldOf(err))
	require.Empty(t, fake.Calls())
}

// TestNullableCursorRejectsNullForRequiredKey keeps the zero SortValue invalid for a key that is not a Presence field.
func TestNullableCursorRejectsNullForRequiredKey(t *testing.T) {
	t.Parallel()

	keys := []sqlbase.ListOrderKey{
		{Column: "age", StorageKind: sqlbase.Integer, Field: listing.Field{Name: "age", Kind: filter.FieldKindInt}},
		{Column: "owner", StorageKind: sqlbase.Text, Field: listing.Field{Name: "owner", Kind: filter.FieldKindString, Presence: true}},
		{Column: "id", StorageKind: sqlbase.Text, Field: listing.Field{Kind: filter.FieldKindString}},
	}
	id, err := pagetoken.NewSortValue("a")
	require.NoError(t, err)
	age, err := pagetoken.NewSortValue(int64(1))
	require.NoError(t, err)

	vals, err := sqlbase.DecodeCursor(keys, pagetoken.Position{Keys: []pagetoken.SortValue{age, {}}, ID: id}, nil)
	require.NoError(t, err)
	require.Equal(t, []any{int64(1), nil, "a"}, vals)

	_, err = sqlbase.DecodeCursor(keys, pagetoken.Position{Keys: []pagetoken.SortValue{{}, {}}, ID: id}, nil)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

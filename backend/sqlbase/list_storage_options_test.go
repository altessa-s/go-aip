// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"strings"
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
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestListExplicitCollation renders text filter operands under the expected collation only when asked.
func TestListExplicitCollation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		opts []sqlbase.ListEngineOption
		want string
	}{
		{nil, `("name" > ($1)::text)`},
		{[]sqlbase.ListEngineOption{sqlbase.WithListExplicitCollation()}, `(("name" COLLATE "C") > (($1)::text COLLATE "C"))`},
		{[]sqlbase.ListEngineOption{sqlbase.WithListExplicitCollation(), sqlbase.WithListTextCollation("und-x-icu")},
			`(("name" COLLATE "und-x-icu") > (($1)::text COLLATE "und-x-icu"))`},
	} {
		db, fake := sqlbasetest.NewFakeDB(t, nil)
		l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"},
			func(id *string) []any { return []any{id} }, tc.opts...)
		_, err := l.List(t.Context(), "items", listing.Request{Filter: `name > "a"`}, nil)
		require.NoError(t, err)
		require.Contains(t, fake.Calls()[0].Query, "WHERE ("+tc.want+")")
	}
}

// TestListStorageOptionRefusals refuses derived columns without a table expression and the PostgreSQL storage choices
// on the other dialects.
func TestListStorageOptionRefusals(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	dest := func(id *string) []any { return []any{id} }

	_, err := postgres.NewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest,
		sqlbase.WithListDerivedColumns("owner_name"))
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)

	_, err = postgres.NewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest,
		sqlbase.WithListDerivedColumns("owner_name"), sqlbase.WithListTableExpression("SELECT i.*, o.name AS owner_name FROM items i JOIN owners o ON o.id = i.owner"))
	require.NoError(t, err)

	for name, opt := range map[string]sqlbase.ListEngineOption{
		"narrow integers":    sqlbase.WithListNarrowIntegers(),
		"explicit collation": sqlbase.WithListExplicitCollation(),
	} {
		_, err = sqlbase.NewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest, mariadb.ListDialect{}, opt)
		require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, "MariaDB "+name)
		_, err = clickhouse.NewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest, opt)
		require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, "ClickHouse "+name)
	}
}

// TestListSchemaKeyColumn tells the columns that order or pin rows from filter-only ones.
func TestListSchemaKeyColumn(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "label", Kind: filter.FieldKindString},
		{Name: "owner", Kind: filter.FieldKindString, Presence: true},
	}, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithSortableFields("name", "owner")))
	c := sqlbase.ListSchemaConfig{Schema: schema, IDColumn: "id", ScopeKeys: []string{"tenant"},
		Columns: map[string]string{"name": "item_name", "tenant": "tenant_id"}}

	for column, want := range map[string]bool{"id": true, "tenant_id": true, "item_name": true, "owner": true, "label": false, "name": false} {
		require.Equal(t, want, c.KeyColumn(column), column)
	}
}

const ownerUUID = "6f1c2d3e-0000-4000-8000-000000000001"

// ownerSchema declares a nullable owner that the profile lets requests sort on.
func ownerSchema(t *testing.T, orders ...string) *listing.Schema {
	t.Helper()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "owner", Kind: filter.FieldKindString, Presence: true},
		{Name: "age", Kind: filter.FieldKindInt},
	}, orders, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithSortFieldValidator(func(listing.Field) error { return nil })))
	require.NoError(t, err)

	return schema
}

// TestListUUIDFields stores a string field in a uuid column: filters cast and validate its operands, and the dialects
// without uuid storage refuse it.
func TestListUUIDFields(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "id"}}
	})
	dest := func(id *string) []any { return []any{id} }
	schema := ownerSchema(t, "age")

	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, dest, sqlbase.WithListUUIDFields(" owner "))
	require.NoError(t, err)

	_, err = l.List(t.Context(), "widgets", listing.Request{Filter: `owner == "` + strings.ToUpper(ownerUUID) + `"`}, nil)
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[0].Query, `("owner" = ($1)::uuid)`)
	require.Equal(t, []any{ownerUUID}, fake.Calls()[0].Args)

	_, err = l.List(t.Context(), "widgets", listing.Request{Filter: `owner == "not-a-uuid"`}, nil)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.Len(t, fake.Calls(), 1, "no query runs for a refused operand")

	for name, opts := range map[string][]sqlbase.ListEngineOption{
		"native identity": {sqlbase.WithListUUIDFields("id")},
		"mapped native identity": {sqlbase.WithListUUIDFields("id"), sqlbase.WithListColumns(map[string]string{"id": "item_id"}),
			sqlbase.WithListIDColumn("item_id")},
		"undeclared": {sqlbase.WithListUUIDFields("tenant")},
		"integer":    {sqlbase.WithListUUIDFields("age")},
	} {
		_, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, dest, opts...)
		require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, name)
	}

	_, err = sqlbase.NewListEngine(db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, dest, mariadb.ListDialect{},
		sqlbase.WithListUUIDFields("owner"))
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)

	config := sqlbase.ListSchemaConfig{UUIDFields: []string{"owner"}}
	require.Equal(t, sqlbase.UUID, config.FieldKind(listing.Field{Name: "owner", Kind: filter.FieldKindString}))
	require.Equal(t, sqlbase.Text, config.FieldKind(listing.Field{Name: "name", Kind: filter.FieldKindString}))
}

func TestDerivedColumnRefusals(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	for _, col := range []string{" ", "not a column", "id"} {
		_, err := postgres.NewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"},
			func(id *string) []any { return []any{id} },
			sqlbase.WithListTableExpression("SELECT * FROM items"), sqlbase.WithListDerivedColumns(col))
		require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, col)
	}
}

func TestKeyColumnWithoutSchema(t *testing.T) {
	t.Parallel()

	c := sqlbase.ListSchemaConfig{IDColumn: "id", ScopeKeys: []string{"tenant"}}
	require.True(t, c.KeyColumn("id"))
	require.True(t, c.KeyColumn("tenant"))
	require.False(t, c.KeyColumn("name"))
}

func TestMixedDirectionUUIDCursor(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "owner", Kind: filter.FieldKindString},
		{Name: "age", Kind: filter.FieldKindInt},
	}, []string{"owner, age desc"}, nil)
	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "owner", "age", "id"},
			Rows: [][]driver.Value{{"a", ownerUUID, int64(2), "a"}, {"b", ownerUUID, int64(1), "b"}}}
	})
	l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), schema, "items", []string{"id"},
		func(id *string) []any { return []any{id} }, sqlbase.WithListUUIDFields("owner"))
	req := listing.Request{PageSize: 1}
	page, err := l.List(t.Context(), "items", req, nil)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextPageToken)
	req.PageToken = page.NextPageToken
	_, err = l.List(t.Context(), "items", req, nil)
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[1].Query, `"_listing"."owner" > $1::uuid`)
	require.Contains(t, fake.Calls()[1].Query, `"_listing"."owner" = $2::uuid`)
}

func TestListStorageConfigComparable(t *testing.T) {
	t.Parallel()

	config := sqlbase.ListStorageConfig{IDType: sqlbase.IDUUID}
	require.True(t, config == (sqlbase.ListStorageConfig{IDType: sqlbase.IDUUID}))
}

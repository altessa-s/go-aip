// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestNullableUUIDSort pages a nullable uuid key in every direction and page size, NULL first ascending and last
// descending, as PostgreSQL orders the whole table.
func TestNullableUUIDSort(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), assetSchema(t, "owner"), "assets", []string{"id"}, idDest, assetOptions()...)
	require.NoError(t, err)
	require.NoError(t, l.CheckSchema(t.Context(), db))
	require.NoError(t, l.EnsureIndexes(t.Context(), db))

	for _, tc := range []struct{ orderBy, sql string }{
		{"owner", "owner ASC NULLS FIRST, id"},
		{"owner desc", "owner DESC NULLS LAST, id DESC"},
		{"owner, kind desc", "owner ASC NULLS FIRST, kind DESC, id DESC"},
		{"kind, owner desc", "kind, owner DESC NULLS LAST, id DESC"},
	} {
		want := query(t, db, "SELECT id::text FROM assets WHERE deleted_at IS NULL ORDER BY "+tc.sql)

		for _, size := range []int32{1, 3, 7, 40} {
			req := listing.Request{PageSize: size, OrderBy: tc.orderBy, Restriction: deletedNull()}
			require.Equal(t, want, walk(t, l, req), "%s, page size %d", tc.orderBy, size)
		}
	}

	owner := "20000000-0000-4000-8000-000000000001"
	want := query(t, db, "SELECT id::text FROM assets WHERE owner = '"+owner+"' OR owner IS NULL ORDER BY owner ASC NULLS FIRST, id")
	got := walk(t, l, listing.Request{PageSize: 2, OrderBy: "owner", Filter: `owner == "` + owner + `" || owner == null`})
	require.Equal(t, want, got)
}

func deletedNull() filter.Node {
	return &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "deleteTime"}, Right: &filter.LiteralNode{}}
}

func assetSchema(t *testing.T, orders ...string) *listing.Schema {
	t.Helper()

	return listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "owner", Kind: filter.FieldKindString, Presence: true},
		{Name: "part", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindInt},
		{Name: "label", Kind: filter.FieldKindString},
		{Name: "createTime", Kind: filter.FieldKindTimestamp},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}, orders, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithServerOnlyFields("deleteTime"),
		listing.WithSortFieldValidator(func(listing.Field) error { return nil })))
}

// assetOptions stores owner and part in uuid columns and accepts the smallint kind and the default collation of label.
func assetOptions() []sqlbase.ListEngineOption {
	return []sqlbase.ListEngineOption{
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListUUIDFields("owner", "part"),
		sqlbase.WithListTimestampPrecision(time.Microsecond),
		sqlbase.WithListColumns(map[string]string{"createTime": "created_at", "deleteTime": "deleted_at"}),
		sqlbase.WithListSchemaCheckPolicy(assetPolicy),
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestStorageOptions accepts the narrower integer column, the filter-only text column outside the C collation and the
// joined column only under their options, keeps rejecting a sorted column outside it, and filters bytewise.
func TestStorageOptions(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	schema := func(sortable ...string) *listing.Schema {
		return listing.MustSchema([]listing.Field{
			{Name: "id", Kind: filter.FieldKindString},
			{Name: "owner", Kind: filter.FieldKindString, Presence: true},
			{Name: "kind", Kind: filter.FieldKindInt},
			{Name: "label", Kind: filter.FieldKindString},
			{Name: "createTime", Kind: filter.FieldKindTimestamp},
		}, []string{"createTime"}, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithSortableFields(sortable...)))
	}
	base := []sqlbase.ListEngineOption{
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListUUIDFields("owner"),
		sqlbase.WithListTimestampPrecision(time.Microsecond), sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}),
	}
	check := func(s *listing.Schema, opts ...sqlbase.ListEngineOption) error {
		l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), s, "assets", []string{"id"}, idDest, append(slices.Clone(base), opts...)...)
		require.NoError(t, err)

		return l.CheckSchema(t.Context(), db)
	}

	both := []sqlbase.ListEngineOption{sqlbase.WithListNarrowIntegers(), sqlbase.WithListExplicitCollation()}
	require.NoError(t, check(schema("createTime", "owner", "kind"), both...))
	require.ErrorIs(t, check(schema("createTime"), sqlbase.WithListExplicitCollation()), sqlbase.ErrInvalidSchema, "kind is smallint")
	require.ErrorIs(t, check(schema("createTime"), sqlbase.WithListNarrowIntegers()), sqlbase.ErrInvalidSchema, "label is not C")
	require.ErrorIs(t, check(schema("createTime", "label"), both...), sqlbase.ErrInvalidSchema, "a sorted label must be C")

	joined := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "partName", Kind: filter.FieldKindString}},
		[]string{"partName"}, nil)
	join := []sqlbase.ListEngineOption{sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListColumns(map[string]string{"partName": "part_name"}),
		sqlbase.WithListTableExpression(`SELECT a.*, p.name AS part_name FROM assets a JOIN parts p ON p.id = a.part`)}
	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), joined, "assets", []string{"id"}, idDest,
		append(slices.Clone(join), sqlbase.WithListDerivedColumns("part_name"))...)
	require.NoError(t, err)
	require.NoError(t, l.EnsureIndexes(t.Context(), db))
	l, err = postgres.NewListEngine(db, pagetokentest.Codec(t), joined, "assets", []string{"id"}, idDest, join...)
	require.NoError(t, err)
	require.ErrorIs(t, l.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema, "part_name is not in assets")

	_, err = db.ExecContext(t.Context(), `UPDATE assets SET label = (ARRAY['a', 'B', 'Z', 'zz'])[i % 4 + 1]
		FROM (SELECT id, row_number() OVER (ORDER BY id) AS i FROM assets) n WHERE assets.id = n.id`)
	require.NoError(t, err)
	want := query(t, db, `SELECT id::text FROM assets WHERE label COLLATE "C" > 'Z' ORDER BY created_at, id`)
	l, err = postgres.NewListEngine(db, pagetokentest.Codec(t), schema("createTime"), "assets", []string{"id"}, idDest,
		append(slices.Clone(base), both...)...)
	require.NoError(t, err)
	require.Equal(t, want, walk(t, l, listing.Request{PageSize: 7, Filter: `label > "Z"`}))

	var collation string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT datcollate FROM pg_database WHERE datname = current_database()`).Scan(&collation))

	if collation != "C" && collation != "POSIX" && collation != "C.UTF-8" {
		require.NotEqual(t, want, query(t, db, `SELECT id::text FROM assets WHERE label > 'Z' ORDER BY created_at, id`),
			"the database collation orders differently, so the filter collates explicitly")
	}
}

func TestNarrowIntegerArrays(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	_, err := db.ExecContext(t.Context(), `ALTER TABLE assets ADD COLUMN ranks smallint[] NOT NULL DEFAULT '{1,2}',
  ADD COLUMN levels integer[] NOT NULL DEFAULT '{1,2}'`)
	require.NoError(t, err)

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindInt},
		{Name: "ranks", Kind: filter.FieldKindInt, Array: true},
		{Name: "levels", Kind: filter.FieldKindInt, Array: true},
	}, []string{"id"}, nil)
	engine := postgres.MustNewListEngine(db, pagetokentest.Codec(t), schema, "assets", []string{"id"}, idDest,
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListNarrowIntegers())
	require.NoError(t, engine.CheckSchema(t.Context(), db))

	for filter, want := range map[string]int64{
		"ranks in [1]": 40, "ranks == 1": 40, "ranks in [32768]": 0,
		"levels in [1]": 40, "levels == 1": 40, "levels in [2147483648]": 0,
		"kind == 32768": 0,
	} {
		got, err := engine.Count(t.Context(), listing.Request{Filter: filter}, nil)
		require.NoError(t, err, filter)
		require.Equal(t, want, got, filter)
	}
}

func TestDerivedScopeColumn(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	scoped := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), scoped, "assets", []string{"id"}, idDest,
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListScopeKeys("part_name"),
		sqlbase.WithListTableExpression(`SELECT a.*, p.name AS part_name FROM assets a JOIN parts p ON p.id = a.part`),
		sqlbase.WithListDerivedColumns("part_name"))
	require.NoError(t, err)
	require.NoError(t, l.EnsureIndexes(t.Context(), db))

	var partName string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT name FROM parts LIMIT 1").Scan(&partName))
	count, err := l.Count(t.Context(), listing.Request{}, sqlbase.Scope{"part_name": partName})
	require.NoError(t, err)
	require.Positive(t, count)
}

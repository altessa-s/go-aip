// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"database/sql/driver"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

type widget struct{ ID, Name, Owner string }

func TestNewListEngine_ClickHouseQuery(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name", "owner"}}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString},
	}, []string{"name"}, nil)
	require.NoError(t, err)

	list, err := clickhouse.NewListEngine[widget](db, nil, schema, "events.widgets", []string{"id", "name", "owner"},
		func(w *widget) []any { return []any{&w.ID, &w.Name, &w.Owner} }, sqlbase.WithListScopeKeys("owner"), sqlbase.WithListParentKey("owner"))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "widgets.List",
		listing.Request{PageSize: 5, Parent: "tenant-a", OrderBy: "name", Filter: `name.startsWith("a")`}, sqlbase.Scope{"owner": "tenant-a"})
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "SELECT `id`, `name`, `id`, `name`, `owner` FROM `events`.`widgets` AS `_listing` WHERE ((startsWith(`name`, ?))) AND "+
		"(`owner` = ?) ORDER BY `_listing`.`name` ASC, `_listing`.`id` ASC LIMIT 6", calls[0].Query)
	require.Equal(t, []any{"a", "tenant-a"}, calls[0].Args)
}

func TestNewListEngine_ClickHouseTypedCursor(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 3, 1, 12, 0, 0, 123000000, time.UTC)

	var seq atomic.Int64

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		if seq.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"id", "price", "created", "id"}, Rows: [][]driver.Value{
				{"z", "10000000000000000.01", created, "z"},
				{"a", "10000000000000000.02", created, "a"},
			}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "price", "created", "id"}}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "price", Kind: filter.FieldKindDecimal}, {Name: "created", Kind: filter.FieldKindTimestamp},
	}, []string{"price, created desc"}, nil)
	require.NoError(t, err)

	type item struct{ ID string }

	l, err := clickhouse.NewListEngine[item](db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, func(r *item) []any { return []any{&r.ID} })
	require.NoError(t, err)

	request := listing.Request{PageSize: 1, OrderBy: "price, created desc", Filter: `price > 0 && created < timestamp("2027-01-01T00:00:00Z")`}
	first, err := l.List(t.Context(), "widgets.List", request, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)

	request.PageToken = first.NextPageToken
	_, err = l.List(t.Context(), "widgets.List", request, nil)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 2)
	require.Contains(t, calls[0].Query, "SELECT `id`, toString(`price`), `created`, `id` FROM `widgets` AS `_listing`")
	require.Contains(t, calls[1].Query, "((`price` > CAST(? AS Decimal(65, 30))) AND (`created` < toDateTime64(?, 3, 'UTC')))")
	require.Contains(t, calls[1].Query, "(`_listing`.`price` > CAST(? AS Decimal(65, 30)) OR (`_listing`.`price` = CAST(? AS Decimal(65, 30)) AND "+
		"(`_listing`.`created` < toDateTime64(?, 9, 'UTC') OR (`_listing`.`created` = toDateTime64(?, 9, 'UTC') AND `_listing`.`id` < ?))))")
	require.Contains(t, calls[1].Query, "ORDER BY `_listing`.`price` ASC, `_listing`.`created` DESC, `_listing`.`id` DESC LIMIT 2")
	require.Equal(t, []any{"0", "2027-01-01 00:00:00.000", "10000000000000000.01", "10000000000000000.01",
		"2026-03-01 12:00:00.123000000", "2026-03-01 12:00:00.123000000", "z"}, calls[1].Args)
}

func TestNewListEngine_ClickHouseRejects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		fields  []listing.Field
		option  sqlbase.ListEngineOption
		message string
	}{
		{name: "uuid", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListIDType(sqlbase.IDUUID)},
		{name: "json arrays", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListArrayStorage(sqlbase.ArraysJSON)},
		{name: "collation", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListTextCollation("utf8mb4_bin")},
		{name: "decimal format", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListDecimalFormat(sqlbase.DecimalFormat{Precision: 80})},
		{name: "uuid fields", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString}},
			option: sqlbase.WithListUUIDFields("owner"), message: "UUID type does not order like the uuid text"},
		{name: "narrow integers", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListNarrowIntegers(),
			message: "64-bit integers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, _ := sqlbasetest.NewFakeDB(t, nil)
			schema, err := listing.NewSchema(tc.fields, []string{"id"}, nil)
			require.NoError(t, err)

			_, err = clickhouse.NewListEngine[widget](db, nil, schema, "widgets", []string{"id"}, func(w *widget) []any { return []any{&w.ID} }, tc.option)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// conformanceFilters are the filters of the listing conformance suite (listingtest), which needs a reachable server to
// run against ClickHouse.
var conformanceFilters = []string{
	`!!(rating > 1)`,
	`!(archived <= false)`,
	`!(archived > false)`,
	`!(note > "note 5")`,
	`!(priority == 0) || name == "name-00"`,
	`!(rating != 1)`,
	`!(rating != null)`,
	`!(rating == 1 || priority == 4)`,
	`!(rating == 1)`,
	`!(rating == null)`,
	`!(rating > 1)`,
	`!(rating in [1, 2])`,
	`!(score.startsWith("a"))`,
	`!(size(note) > 6)`,
	`!(size(score) == 0)`,
	`!(size(score) > 0)`,
	`!(size(tags) <= 1)`,
	`!(tags == "t1")`,
	`"u2" in tags`,
	`archived < true`,
	`archived == true`,
	`archived == true || archived == false`,
	`archived > false`,
	`archived >= true`,
	`contains(name, "-0") && endsWith(name, "3")`,
	`createTime <= timestamp("2026-03-01T12:03:00.123456Z")`,
	`createTime == timestamp("2026-03-01T12:05:00.123999Z")`,
	`createTime == timestamp("2026-03-01T12:05:00.123Z")`,
	`createTime > timestamp("2026-03-01T12:10:00.1231Z")`,
	`createTime >= timestamp("2026-03-01T12:20:00.1239Z")`,
	`flag == false`,
	`kind != "KIND_A"`,
	`kind != "KIND_A" && archived == false`,
	`kind == "KIND_B"`,
	`kind in ["KIND_A", "KIND_C"] && priority >= 2`,
	`kind2 != null`,
	`kind2 == "KIND_UNSPECIFIED"`,
	`kind2 == null`,
	`level == 0`,
	`name.contains("-0") && priority < 3`,
	`name.startsWith("name-1")`,
	`note != "note 0"`,
	`note != null`,
	`note < "n"`,
	`note == ""`,
	`note == null`,
	`note > "note 5"`,
	`note > "note 5" || priority == 4`,
	`priority > 1`,
	`rating != 1`,
	`rating != 1 && priority == 4`,
	`rating != null`,
	`rating == 0`,
	`rating == 1`,
	`rating == null`,
	`score == ""`,
	`search("NOTE 1")`,
	`size(note) != 0`,
	`size(note) < 1`,
	`size(note) <= 0 || priority == 4`,
	`size(note) == 0`,
	`size(note) == 2 && note.startsWith("界")`,
	`size(note) == 7 && priority == 0`,
	`size(note) > 6`,
	`size(note) >= 0 && kind == "KIND_A"`,
	`size(score) == 0`,
	`size(tags) != 2`,
	`size(tags) < 1 && priority != 0`,
	`size(tags) == 0`,
	`size(tags) == 2 && kind == "KIND_B"`,
	`startsWith(name, "name-1")`,
	`tags != "t1"`,
	`tags == "t1"`,
	`tags == "t2" && tags == "u1"`,
	`tags == null`,
	`tags > "u3"`,
	`tags in ["t0", "u4"]`,
	`tags.startsWith("u0")`,
}

// TestNewListEngine_ConformanceFixture serves the widget fixture of the listing conformance suite (every field kind,
// Optional and Presence fields, a repeated field, enums and a custom function) and translates every filter of the
// suite into ClickHouse SQL.
func TestNewListEngine_ConformanceFixture(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id"}}
	})
	l, err := clickhouse.NewListEngine[listingtest.Widget](db, pagetokentest.Codec(t), listingtest.NewSchema(t), "widgets", []string{"_id"},
		func(w *listingtest.Widget) []any { return []any{&w.ID} }, sqlbase.WithListIDColumn("_id"), sqlbase.WithListColumns(listingtest.FieldPaths()))
	require.NoError(t, err)

	for _, expr := range conformanceFilters {
		_, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 3, Filter: expr}, nil)
		require.NoError(t, err, expr)
	}

	for _, tc := range []struct{ filter, sql string }{
		{`note == null`, "(`note` = ?)"},
		{`rating == null`, "((`rating`) IS NULL)"},
		{`!(rating == 1)`, "(`rating` != ? OR `rating` IS NULL)"},
		{`!(size(score) > 0)`, "(NOT (COALESCE(lengthUTF8(`score`), 0) > ?))"},
		{`tags == null`, "empty(`tags`)"},
		{`tags in ["t0", "u4"]`, "hasAny(`tags`, [?, ?])"},
		{`kind == "KIND_B"`, "(`kind` = ?)"},
		{`createTime == timestamp("2026-03-01T12:05:00.123999Z")`, "(`created_at` = toDateTime64(?, 3, 'UTC'))"},
	} {
		_, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 3, Filter: tc.filter}, nil)
		require.NoError(t, err, tc.filter)

		calls := fake.Calls()
		require.Contains(t, calls[len(calls)-1].Query, tc.sql, tc.filter)
	}

	for _, expr := range []string{`kind > "KIND_A"`, `kind in ["KIND_A", "KIND_Z"]`} {
		_, err := l.List(t.Context(), listingtest.Subject, listing.Request{Filter: expr}, nil)
		require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err), expr)
	}
}

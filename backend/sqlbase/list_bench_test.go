// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func BenchmarkBuildQuery(b *testing.B) {
	db, fake := sqlbasetest.NewFakeDB(b, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name"}}
	})
	fake.DisableRecording()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(b, err)
	list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id", "name"}, func(r *row) []any { return []any{&r.ID, &r.Name} }, postgres.ListDialect{})
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = list.List(b.Context(), "widgets.List", listing.Request{PageSize: 20, OrderBy: "name", Filter: `name == "alpha"`}, nil)
	}
}

func BenchmarkMariaDBBuildQuery(b *testing.B) {
	db, fake := sqlbasetest.NewFakeDB(b, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name"}}
	})
	fake.DisableRecording()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(b, err)
	list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id", "name"}, func(r *row) []any { return []any{&r.ID, &r.Name} }, mariadb.ListDialect{})
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = list.List(b.Context(), "widgets.List", listing.Request{PageSize: 20, OrderBy: "name", Filter: `name == "alpha"`}, nil)
	}
}

func BenchmarkDialectCursorPredicate(b *testing.B) {
	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		b.Run(fmt.Sprintf("%T", dialect), func(b *testing.B) {
			id, err := pagetoken.NewSortValue("a")
			require.NoError(b, err)
			value, err := pagetoken.NewSortValue(int64(4))
			require.NoError(b, err)

			keys := []sqlbase.ListOrderKey{{Column: "value", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindInt), Field: listing.Field{Kind: filter.FieldKindInt}}, {Column: "id", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindString), Field: listing.Field{Kind: filter.FieldKindString}}}
			pos := pagetoken.Position{ID: id, Keys: []pagetoken.SortValue{value}}
			format := sqlbase.DefaultDecimalFormat()

			b.ReportAllocs()

			for b.Loop() {
				_, _, err := dialect.CursorPredicate(keys, pos, "source", sqlbase.IDNative, format)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchmarkPageRows is the page size of the scanning benchmarks; the fake returns one extra row, so a token is issued.
const benchmarkPageRows = 100

func benchmarkPageList(b *testing.B) *sqlbase.ListEngine[row] {
	b.Helper()

	rows := make([][]driver.Value, benchmarkPageRows+1)
	for i := range rows {
		id := fmt.Sprintf("w%04d", i)
		rows[i] = []driver.Value{id, int64(i), id, "name", int64(i)}
	}

	db, fake := sqlbasetest.NewFakeDB(b, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "age", "id", "name", "age"}, Rows: rows}
	})
	fake.DisableRecording()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString},
		{Name: "age", Kind: filter.FieldKindInt}}, []string{"age"}, nil)
	require.NoError(b, err)
	list, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(b), schema, "widgets", []string{"id", "name", "age"},
		func(r *row) []any { return []any{&r.ID, &r.Name, &r.Age} }, postgres.ListDialect{})
	require.NoError(b, err)

	return list
}

func BenchmarkListPage(b *testing.B) {
	list := benchmarkPageList(b)
	req := listing.Request{PageSize: benchmarkPageRows, OrderBy: "age", Filter: `name == "name"`}

	b.ReportAllocs()

	for b.Loop() {
		out, err := list.List(b.Context(), "widgets.List", req, nil)
		if err != nil || out.NextPageToken == "" {
			b.Fatal(err)
		}
	}
}

func BenchmarkListResume(b *testing.B) {
	list := benchmarkPageList(b)
	req := listing.Request{PageSize: benchmarkPageRows, OrderBy: "age", Filter: `name == "name"`}
	first, err := list.List(b.Context(), "widgets.List", req, nil)
	require.NoError(b, err)

	req.PageToken = first.NextPageToken

	b.ReportAllocs()

	for b.Loop() {
		if _, err := list.List(b.Context(), "widgets.List", req, nil); err != nil {
			b.Fatal(err)
		}
	}
}

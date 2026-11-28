// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// joinedSource is a derived table adding the name of each asset's part.
const joinedSource = `SELECT a.id AS id, a.level AS level, p.name AS part_name FROM assets AS a INNER JOIN parts AS p ON p.id = a.part`

// TestTableExpression reads List and Count from a join, while CheckSchema names the base table and skips the
// derived column.
func TestTableExpression(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)

	for _, stmt := range []string{
		`CREATE TABLE parts (id String, name String) ENGINE = MergeTree ORDER BY id`,
		`CREATE TABLE assets (id String, part String, level Int64) ENGINE = MergeTree ORDER BY id`,
	} {
		_, err := db.ExecContext(t.Context(), stmt)
		require.NoError(t, err)
	}

	insertRows(t, db, "parts", []string{"id", "name"}, [][]any{{"p1", "part-1"}, {"p2", "part-2"}, {"p3", "part-3"}})

	assets := make([][]any, 0, 12)
	for i := range 12 {
		assets = append(assets, []any{fmt.Sprintf("a%02d", i), fmt.Sprintf("p%d", i%3+1), int64(i % 4)})
	}

	insertRows(t, db, "assets", []string{"id", "part", "level"}, assets)

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "partName", Kind: filter.FieldKindString},
		{Name: "level", Kind: filter.FieldKindInt},
	}, []string{"partName, level"}, nil)
	options := []sqlbase.ListEngineOption{sqlbase.WithListColumns(map[string]string{"partName": "part_name"}), sqlbase.WithListTableExpression(joinedSource)}
	l, err := clickhouse.NewListEngine(db, pagetokentest.Codec(t), schema, "assets", []string{"id"}, func(id *string) []any { return []any{id} },
		append(options, sqlbase.WithListDerivedColumns("part_name"))...)
	require.NoError(t, err)
	require.NoError(t, l.CheckSchema(t.Context(), db))

	want := selectColumn(t, db,
		`SELECT a.id FROM assets AS a INNER JOIN parts AS p ON p.id = a.part WHERE p.name > 'part-1' ORDER BY p.name, a.level, a.id`)
	require.Len(t, want, 8)

	const restrict = `partName > "part-1"`

	for _, size := range []int32{1, 3, 8} {
		require.Equal(t, want, walkIDs(t, l, "assets", listing.Request{PageSize: size, Filter: restrict}), "page size %d", size)
	}

	total, err := l.Count(t.Context(), listing.Request{Filter: restrict}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(len(want)), total)

	strict, err := clickhouse.NewListEngine(db, pagetokentest.Codec(t), schema, "assets", []string{"id"}, func(id *string) []any { return []any{id} },
		options...)
	require.NoError(t, err)
	require.ErrorIs(t, strict.CheckSchema(t.Context(), db), listing.ErrInvalidSchema, "the base table has no part_name")
}

// walkIDs reads every page of req and returns the ids in page order.
func walkIDs(t *testing.T, l *sqlbase.ListEngine[string], parent string, req listing.Request) []string {
	t.Helper()

	var ids []string

	for {
		p, err := l.List(t.Context(), parent, req, nil)
		require.NoError(t, err)

		ids = append(ids, p.Items...)
		if p.NextPageToken == "" {
			return ids
		}

		req.PageToken = p.NextPageToken
	}
}

// selectColumn returns the first column of every row of query.
func selectColumn(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), query)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []string

	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}

	require.NoError(t, rows.Err())

	return out
}

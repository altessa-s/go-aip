// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// widgetColumns are the stored columns of listingtest.Widget, in widgetDest order.
var widgetColumns = []string{
	"id", "name", "kind", "priority", "created_at", "note", "tags", "archived", "flag", "level", "kind2", "rating", "score",
}

// widgetDDL is the listing storage contract for listingtest.Widget on ClickHouse: String text (compared by bytes), Int64
// integers, Required and Optional fields non-Nullable (an absent optional value stored as its zero value), presence fields
// Nullable, DateTime64(3) and a native Array(String). The MergeTree sorting key starts with the dominant sort pattern.
const widgetDDL = `CREATE TABLE %s (
	id String,
	name String,
	kind LowCardinality(String),
	priority Int64,
	created_at DateTime64(3, 'UTC'),
	note String,
	tags Array(String),
	archived Bool,
	flag Bool,
	level Int64,
	kind2 String,
	rating Nullable(Int64),
	score Nullable(String)
) ENGINE = MergeTree ORDER BY (created_at, id)`

func widgetDest(w *listingtest.Widget) []any {
	return []any{&w.ID, &w.Name, &w.Kind, &w.Priority, &w.Created, &w.Note, &w.Tags, &w.Archived, &w.Flag, &w.Level, &w.Kind2,
		&w.Rating, &w.Score}
}

// TestListEngineConformance runs the listing engine conformance suite against real ClickHouse MergeTree tables: every order
// and page size, every contract filter, token binding and request validation, executed by the server.
func TestListEngineConformance(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)

	var tables atomic.Int64

	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		t.Helper()

		table := fmt.Sprintf("widgets_%d", tables.Add(1))
		storeWidgets(t, db, table, items)

		l, err := clickhouse.NewListEngine[listingtest.Widget](db, codec, s, table, widgetColumns, widgetDest,
			sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}))
		require.NoError(t, err)
		require.NoError(t, l.EnsureIndexes(t.Context(), db), "the table follows the listing storage contract")

		return func(ctx context.Context, subject string, req listing.Request) (page.Page[listingtest.Widget], error) {
			return l.List(ctx, subject, req, nil)
		}
	})
}

// storeWidgets creates table and stores items in it under the listing storage contract.
func storeWidgets(t *testing.T, db *sql.DB, table string, items []listingtest.Widget) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), fmt.Sprintf(widgetDDL, table))
	require.NoError(t, err)

	rows := make([][]any, 0, len(items))
	for _, w := range items {
		tags := w.Tags
		if tags == nil {
			tags = []string{}
		}

		rows = append(rows, []any{w.ID, w.Name, w.Kind, w.Priority, w.Created.Truncate(listing.TimestampPrecision), zero(w.Note), tags,
			w.Archived, zero(w.Flag), zero(w.Level), zero(w.Kind2), w.Rating, w.Score})
	}

	insertRows(t, db, table, widgetColumns, rows)
}

// insertRows stores rows (values in columns order) in table with one batch.
func insertRows(t *testing.T, db *sql.DB, table string, columns []string, rows [][]any) {
	t.Helper()

	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	stmt, err := tx.PrepareContext(t.Context(), fmt.Sprintf("INSERT INTO %s (%s)", table, strings.Join(columns, ", ")))
	require.NoError(t, err)

	for _, row := range rows {
		_, err := stmt.ExecContext(t.Context(), row...)
		require.NoError(t, err, "row %v", row)
	}

	require.NoError(t, tx.Commit())
}

// zero returns the value of an optional field, or its zero value when it is absent: the stored form of an Optional field.
func zero[T any](v *T) T {
	if v == nil {
		var z T

		return z
	}

	return *v
}

// storeDDL is a table under the listing storage contract for storeSchema, scoped by tenant: an exact Decimal, a millisecond
// DateTime64, a non-Nullable Optional string, a Nullable presence integer and an Array of LowCardinality strings. %s is the
// table, %s each column definition the schema checks replace.
const storeDDL = `CREATE TABLE %s (id String, tenant %s, name %s, price %s, created %s, note %s, rating %s, tags %s)
	ENGINE = MergeTree ORDER BY id`

// storeColumns are the column types of storeDDL that follow the contract, in its order after the table.
var storeColumns = []string{"String", "String", "Decimal(38, 10)", "DateTime64(3, 'UTC')", "String", "Nullable(Int64)",
	"Array(LowCardinality(String))"}

// storeSchema is the listing schema of storeDDL.
func storeSchema(t *testing.T) *listing.Schema {
	t.Helper()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "price", Kind: filter.FieldKindDecimal},
		{Name: "created", Kind: filter.FieldKindTimestamp},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"price desc, created", "created desc", "name"}, nil)
	require.NoError(t, err)

	return s
}

type storeItem struct{ ID, Name string }

// newStore builds the list engine of storeSchema over table, scoped by tenant.
func newStore(t *testing.T, db *sql.DB, table string, codec pagetoken.Signer) *sqlbase.ListEngine[storeItem] {
	t.Helper()

	l, err := clickhouse.NewListEngine[storeItem](db, codec, storeSchema(t), table, []string{"id", "name"},
		func(i *storeItem) []any { return []any{&i.ID, &i.Name} }, sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant"))
	require.NoError(t, err)

	return l
}

// TestListEngineSchemaLive checks the ClickHouse storage contract on real tables: CheckSchema accepts a conforming table and
// rejects each departure from it, EnsureIndexes adds one minmax skipping index per sort pattern (and is idempotent), and the
// typed cursors page a decimal and a millisecond timestamp key within a scope.
func TestListEngineSchemaLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	create := func(t *testing.T, table string, override map[int]string) {
		t.Helper()

		args := []any{table}

		for i, c := range storeColumns {
			if o, ok := override[i]; ok {
				c = o
			}

			args = append(args, c)
		}

		_, err := db.ExecContext(t.Context(), fmt.Sprintf(storeDDL, args...))
		require.NoError(t, err)
	}

	create(t, "store", nil)

	l := newStore(t, db, "store", pagetokentest.Codec(t))
	require.NoError(t, l.EnsureIndexes(t.Context(), db))
	require.NoError(t, l.EnsureIndexes(t.Context(), db), "EnsureIndexes is idempotent")

	var indexes []string

	rows, err := db.QueryContext(t.Context(), `SELECT expr FROM system.data_skipping_indices
		WHERE database = currentDatabase() AND table = 'store' AND type = 'minmax' ORDER BY name`)
	require.NoError(t, err)

	for rows.Next() {
		var expr string
		require.NoError(t, rows.Scan(&expr))

		indexes = append(indexes, expr)
	}

	require.NoError(t, errors.Join(rows.Err(), rows.Close()))
	require.ElementsMatch(t, []string{"tenant, price, created, id", "tenant, created, id", "tenant, name, id"}, indexes)

	t.Run("pages typed keys", func(t *testing.T) {
		t.Parallel()

		base := time.Date(2026, 3, 1, 12, 0, 0, 123000000, time.UTC)

		var stored [][]any

		for i := range 12 {
			tenant := []string{"a", "b"}[i%2]
			price := decimal.RequireFromString([]string{"10000000000000000.01", "10000000000000000.02", "0.0000000001"}[i%3])
			stored = append(stored, []any{fmt.Sprintf("s-%02d", i), tenant, fmt.Sprintf("n-%d", i%4), &price,
				base.Add(time.Duration(i%2) * time.Millisecond), "", nil, []string{}})
		}

		insertRows(t, db, "store", []string{"id", "tenant", "name", "price", "created", "note", "rating", "tags"}, stored)

		var got []string

		req := listing.Request{PageSize: 1, Parent: "a", OrderBy: "price desc, created"}
		for range 20 {
			p, err := l.List(t.Context(), "stores.List", req, sqlbase.Scope{"tenant": "a"})
			require.NoError(t, err)

			for _, item := range p.Items {
				got = append(got, item.ID)
			}

			if p.NextPageToken == "" {
				break
			}

			req.PageToken = p.NextPageToken
		}

		// Tenant a holds the even ids; created alternates by id parity, so ties on price break by created, then id.
		require.Equal(t, []string{"s-04", "s-10", "s-00", "s-06", "s-02", "s-08"}, got)
	})

	for name, override := range map[string]map[int]string{
		"nullable optional":      {4: "Nullable(String)"},
		"non-nullable presence":  {5: "Int64"},
		"nullable elements":      {6: "Array(Nullable(String))"},
		"timestamp precision":    {3: "DateTime64(6, 'UTC')"},
		"decimal domain":         {2: "Decimal(76, 40)"},
		"type":                   {1: "Int64"},
		"array type":             {6: "String"},
		"nullable scope":         {0: "Nullable(String)"},
		"nullable integer value": {5: "Nullable(String)"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			table := "bad_" + strings.NewReplacer(" ", "_", "-", "_").Replace(name)
			create(t, table, override)
			require.ErrorIs(t, newStore(t, db, table, nil).CheckSchema(t.Context(), db), listing.ErrInvalidSchema)
		})
	}

	t.Run("missing column", func(t *testing.T) {
		t.Parallel()

		_, err := db.ExecContext(t.Context(), `CREATE TABLE bad_missing (id String, tenant String, name String, price Decimal(38, 10),
			created DateTime64(3, 'UTC'), note String, tags Array(String)) ENGINE = MergeTree ORDER BY id`)
		require.NoError(t, err)
		require.ErrorIs(t, newStore(t, db, "bad_missing", nil).CheckSchema(t.Context(), db), listing.ErrInvalidSchema)
	})
}

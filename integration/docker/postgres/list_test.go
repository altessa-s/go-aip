// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// widgetColumns are the stored columns of listingtest.Widget, in widgetDest order.
var widgetColumns = []string{
	"id", "name", "kind", "priority", "created_at", "note", "tags", "archived", "flag", "level", "kind2", "rating", "score",
}

// widgetDDL is the listing storage contract for listingtest.Widget: text in COLLATE "C", Required and Optional fields NOT NULL
// (an absent optional value stored as its zero value), presence fields nullable, timestamps at millisecond precision and a
// NOT NULL array without NULL elements.
const widgetDDL = `CREATE TABLE %s (
	id text COLLATE "C" PRIMARY KEY,
	name text COLLATE "C" NOT NULL,
	kind text COLLATE "C" NOT NULL,
	priority bigint NOT NULL,
	created_at timestamptz(3) NOT NULL,
	note text COLLATE "C" NOT NULL,
	tags text[] COLLATE "C" NOT NULL,
	archived boolean NOT NULL,
	flag boolean NOT NULL,
	level bigint NOT NULL,
	kind2 text COLLATE "C" NOT NULL,
	rating bigint,
	score text COLLATE "C"
)`

func widgetDest(w *listingtest.Widget) []any {
	return []any{&w.ID, &w.Name, &w.Kind, &w.Priority, &w.Created, &w.Note, &w.Tags, &w.Archived, &w.Flag, &w.Level, &w.Kind2,
		&w.Rating, &w.Score}
}

// TestListEngineConformance runs the listing engine conformance suite against real PostgreSQL tables: every order and page
// size, every contract filter, token binding and request validation, executed by the server.
func TestListEngineConformance(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)

	var tables atomic.Int64

	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		t.Helper()

		table := fmt.Sprintf("widgets_%d", tables.Add(1))
		storeWidgets(t, db, table, items)

		l, err := postgres.NewListEngine[listingtest.Widget](db, codec, s, table, widgetColumns, widgetDest,
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

	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)", table, strings.Join(widgetColumns, ", "))

	for _, w := range items {
		tags := w.Tags
		if tags == nil {
			tags = []string{}
		}

		_, err := db.ExecContext(t.Context(), insert, w.ID, w.Name, w.Kind, w.Priority, w.Created.Truncate(listing.TimestampPrecision),
			zero(w.Note), tags, w.Archived, zero(w.Flag), zero(w.Level), zero(w.Kind2), w.Rating, w.Score)
		require.NoError(t, err)
	}
}

// zero returns the value of an optional field, or its zero value when it is absent: the stored form of an Optional field.
func zero[T any](v *T) T {
	if v == nil {
		var z T

		return z
	}

	return *v
}

func TestCutoffCount(t *testing.T) {
	t.Parallel()
	db := dbtest.Postgres(t)
	items := listingtest.MakeWidgets(60)
	storeWidgets(t, db, "widgets", items)
	engine, err := postgres.NewListEngine(db, nil, listingtest.NewSchema(t), "widgets", widgetColumns, widgetDest,
		sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}))
	require.NoError(t, err)
	listingtest.RunCutoffCount(t, items, engine.Count, nil)
}

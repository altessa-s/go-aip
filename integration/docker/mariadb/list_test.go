// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mariadb_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
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

// widgetDDL is the listing storage contract for listingtest.Widget on MariaDB: utf8mb4_nopad_bin text (the database default),
// indexable VARCHAR sort columns, signed BIGINT integers, Required and Optional fields NOT NULL (an absent optional value
// stored as its zero value), presence fields nullable, DATETIME(3) and a NOT NULL JSON array, whose utf8mb4_bin default
// collation is replaced too.
const widgetDDL = `CREATE TABLE %s (
	id VARCHAR(16) NOT NULL PRIMARY KEY,
	name VARCHAR(64) NOT NULL,
	kind VARCHAR(8) NOT NULL,
	priority BIGINT NOT NULL,
	created_at DATETIME(3) NOT NULL,
	note VARCHAR(64) NOT NULL,
	tags JSON COLLATE utf8mb4_nopad_bin NOT NULL,
	archived BOOLEAN NOT NULL,
	flag BOOLEAN NOT NULL,
	level BIGINT NOT NULL,
	kind2 VARCHAR(8) NOT NULL,
	rating BIGINT,
	score VARCHAR(64)
)`

func widgetDest(w *listingtest.Widget) []any {
	return []any{&w.ID, &w.Name, &w.Kind, &w.Priority, &w.Created, &w.Note, jsonStrings{&w.Tags}, &w.Archived, &w.Flag, &w.Level,
		&w.Kind2, &w.Rating, &w.Score}
}

// jsonStrings scans a JSON array of strings.
type jsonStrings struct{ dst *[]string }

func (j jsonStrings) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, j.dst)
	case string:
		return json.Unmarshal([]byte(v), j.dst)
	default:
		return fmt.Errorf("scan %T into a JSON array", src)
	}
}

// TestListEngineConformance runs the listing engine conformance suite against real MariaDB tables: every order and page size,
// every contract filter, token binding and request validation, executed by the server.
func TestListEngineConformance(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)

	var tables atomic.Int64

	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		t.Helper()

		table := fmt.Sprintf("widgets_%d", tables.Add(1))
		storeWidgets(t, db, table, items)

		l, err := mariadb.NewListEngine[listingtest.Widget](db, codec, s, table, widgetColumns, widgetDest,
			sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}), sqlbase.WithListArrayStorage(sqlbase.ArraysJSON))
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

	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(widgetColumns, ", "),
		strings.TrimSuffix(strings.Repeat("?, ", len(widgetColumns)), ", "))

	for _, w := range items {
		tags := w.Tags
		if tags == nil {
			tags = []string{}
		}

		encoded, err := json.Marshal(tags)
		require.NoError(t, err)

		_, err = db.ExecContext(t.Context(), insert, w.ID, w.Name, w.Kind, w.Priority, w.Created.Truncate(listing.TimestampPrecision),
			zero(w.Note), string(encoded), w.Archived, zero(w.Flag), zero(w.Level), zero(w.Kind2), w.Rating, w.Score)
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

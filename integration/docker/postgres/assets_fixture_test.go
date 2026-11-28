// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
)

// assetDDL stores a nullable uuid owner, a smallint kind and a text label without the C collation; the parts table
// adds the name the joined listing reads.
const assetDDL = `CREATE TABLE parts (id uuid PRIMARY KEY, name text COLLATE "C" NOT NULL);
CREATE TABLE assets (
	id uuid PRIMARY KEY,
	owner uuid,
	part uuid NOT NULL REFERENCES parts,
	kind smallint NOT NULL,
	label text NOT NULL,
	deleted_at timestamptz,
	created_at timestamptz NOT NULL
);
INSERT INTO parts SELECT ('00000000-0000-4000-8000-' || lpad(p::text, 12, '0'))::uuid, 'part-' || p FROM generate_series(1, 3) p;
INSERT INTO assets SELECT ('10000000-0000-4000-8000-' || lpad(i::text, 12, '0'))::uuid,
	CASE WHEN i % 3 = 0 THEN NULL ELSE ('20000000-0000-4000-8000-' || lpad((i % 4)::text, 12, '0'))::uuid END,
	('00000000-0000-4000-8000-' || lpad((i % 3 + 1)::text, 12, '0'))::uuid, i % 5, 'label-' || i,
	CASE WHEN i % 7 = 0 THEN now() END, timestamptz '2026-01-01' + i * interval '1 second'
FROM generate_series(1, 40) i`

func assetStore(t *testing.T) *sql.DB {
	t.Helper()

	db := dbtest.Postgres17(t)
	_, err := db.ExecContext(t.Context(), assetDDL)
	require.NoError(t, err)

	return db
}

func assetPolicy(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
	if (r.Check == sqlbase.CheckType && r.Column == "kind") || (r.Check == sqlbase.CheckCollation && r.Column == "label") {
		return nil
	}

	return r.Err
}

func idDest(id *string) []any { return []any{id} }

// walk reads every page of req and returns the ids in page order.
func walk(t *testing.T, l *sqlbase.ListEngine[string], req listing.Request) []string {
	t.Helper()

	var ids []string

	for {
		p, err := l.List(t.Context(), "assets", req, nil)
		require.NoError(t, err)

		ids = append(ids, p.Items...)
		if p.NextPageToken == "" {
			return ids
		}

		req.PageToken = p.NextPageToken
	}
}

func query(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), q)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var ids []string

	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}

	require.NoError(t, rows.Err())

	return ids
}

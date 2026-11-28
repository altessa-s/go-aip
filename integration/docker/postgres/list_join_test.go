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

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestJoinedListing reads a join as the table expression; CheckSchema names the base table, so the policy accepts
// the joined column there.
func TestJoinedListing(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "partName", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindInt},
	}, []string{"partName, kind"}, nil)
	policy := func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
		if (r.Check == sqlbase.CheckColumn && r.Column == "part_name") || (r.Check == sqlbase.CheckType && r.Column == "kind") {
			return nil
		}

		return r.Err
	}
	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "assets", []string{"id"}, idDest,
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListColumns(map[string]string{"partName": "part_name"}),
		sqlbase.WithListTableExpression(`SELECT a.*, p.name AS part_name FROM assets a JOIN parts p ON p.id = a.part`),
		sqlbase.WithListSchemaCheckPolicy(policy))
	require.NoError(t, err)
	require.NoError(t, l.CheckSchema(t.Context(), db))

	want := query(t, db, `SELECT a.id::text FROM assets a JOIN parts p ON p.id = a.part WHERE p.name > 'part-1' ORDER BY p.name, a.kind, a.id`)
	require.Equal(t, want, walk(t, l, listing.Request{PageSize: 4, Filter: `partName > "part-1"`}))

	total, err := l.Count(t.Context(), listing.Request{Filter: `partName > "part-1"`}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(len(want)), total)

	strict, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "assets", []string{"id"}, idDest,
		sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListColumns(map[string]string{"partName": "part_name"}))
	require.NoError(t, err)
	require.ErrorIs(t, strict.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema, "the base table has no part_name")
}

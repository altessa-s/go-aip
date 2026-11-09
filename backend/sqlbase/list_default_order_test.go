// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestMariaDefaultOrderIndexability(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, nil, listing.NewProfile(listing.WithEmptyOrderAllowed(true), listing.WithUnrestrictedOrder(true), listing.WithDefaultOrderBy("name")))
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"}, Rows: [][]driver.Value{
			{"id", "varchar", "varchar(64)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil},
			{"name", "text", "text", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil},
		}}
	})
	engine, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(s *string) []any { return []any{s} }, mariadb.ListDialect{})
	require.NoError(t, err)
	err = engine.CheckSchema(t.Context(), db)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Contains(t, err.Error(), "indexed column")
}

func TestNewValidatesUnrestrictedDefaultOrder(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, nil,
		listing.NewProfile(listing.WithEmptyOrderAllowed(true), listing.WithUnrestrictedOrder(true), listing.WithDefaultOrderBy("id, name")))
	require.NoError(t, err)
	_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

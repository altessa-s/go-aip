// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func countSchema(t *testing.T) *listing.Schema {
	t.Helper()

	return listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString},
	}, []string{"name"}, nil)
}

// TestListTableExpression reads List and Count from the derived table, while indexes and schema checks name the table.
func TestListTableExpression(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.Contains(query, "COUNT(*)") {
			return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(1)}}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "name", "id"}, Rows: [][]driver.Value{{"a", "x", "a"}}}
	})

	const source = `SELECT w.*, o.name FROM widgets w JOIN owners o ON o.id = w.owner_id`

	l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "widgets", []string{"id"},
		func(id *string) []any { return []any{id} }, sqlbase.WithListTableExpression("  "+source+"  "))

	page, err := l.List(t.Context(), "widgets", listing.Request{Filter: `name == "x"`}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, page.Items)

	_, err = l.Count(t.Context(), listing.Request{Filter: `name == "x"`}, nil)
	require.NoError(t, err)

	from := ` FROM (` + source + `) AS "_listing" WHERE (("name" = ($1)::text))`
	for _, call := range fake.Calls() {
		require.Contains(t, call.Query, from)
	}

	require.Len(t, fake.Calls(), 2)
	require.Contains(t, l.Indexes()[0], `ON "widgets" ("name" ASC, "id" ASC)`)
}

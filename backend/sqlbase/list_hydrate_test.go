// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestListHydrated loads the rows of the listed keys in page order, counts the matches only when asked and skips the
// load of an empty page.
func TestListHydrated(t *testing.T) {
	t.Parallel()

	keys := [][]driver.Value{{"b", "x", "b"}, {"a", "x", "a"}}
	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.Contains(query, "COUNT(*)") {
			return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(len(keys))}}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "name", "id"}, Rows: keys}
	})
	l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "widgets", []string{"id"},
		func(id *string) []any { return []any{id} })

	var loaded [][]string

	load := func(_ context.Context, ids []string) ([]string, error) {
		loaded = append(loaded, ids)

		rows := make([]string, len(ids))
		for i, id := range ids {
			rows[i] = "row " + id
		}

		return rows, nil
	}

	page, err := sqlbase.ListHydrated(t.Context(), l, "widgets", listing.Request{Filter: `name == "x"`}, nil, load, true)
	require.NoError(t, err)
	require.Equal(t, []string{"row b", "row a"}, page.Items)
	require.Equal(t, &sqlbase.Total{Count: 2, Exact: true}, page.Total)
	require.Len(t, fake.Calls(), 2)

	page, err = sqlbase.ListHydrated(t.Context(), l, "widgets", listing.Request{Filter: `name == "x"`}, nil, load, false)
	require.NoError(t, err)
	require.Nil(t, page.Total)
	require.Len(t, fake.Calls(), 3, "no count without asking")

	keys = nil
	page, err = sqlbase.ListHydrated(t.Context(), l, "widgets", listing.Request{}, nil, load, false)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Len(t, loaded, 2, "an empty page loads nothing")

	broken := errors.New("load failed")
	keys = [][]driver.Value{{"a", "x", "a"}}
	_, err = sqlbase.ListHydrated(t.Context(), l, "widgets", listing.Request{}, nil,
		func(context.Context, []string) ([]string, error) { return nil, broken }, true)
	require.ErrorIs(t, err, broken)
}

func TestListHydratedRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name", "id"}, Rows: [][]driver.Value{{"a", "x", "a"}}}
	})
	engine := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "widgets", []string{"id"},
		func(id *string) []any { return []any{id} })
	load := func(context.Context, []string) ([]string, error) {
		t.Error("invalid input called the loader")
		return []string{"row a"}, nil
	}

	for _, tc := range []struct {
		name   string
		engine *sqlbase.ListEngine[string]
		load   func(context.Context, []string) ([]string, error)
		opts   []sqlbase.CountOption
	}{
		{name: "nil engine", load: load},
		{name: "nil loader", engine: engine},
		{name: "invalid count option", engine: engine, load: load, opts: []sqlbase.CountOption{sqlbase.WithCountLimit(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := sqlbase.ListHydrated(t.Context(), tc.engine, "widgets", listing.Request{}, nil, tc.load, true, tc.opts...)
				require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)
			})
			require.Empty(t, fake.Calls())
		})
	}
}

func TestListHydratedPreservesPage(t *testing.T) {
	t.Parallel()

	countErr := errors.New("count failed")
	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.Contains(query, "COUNT(*)") {
			return sqlbasetest.Reply{Err: countErr}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "name", "id"}, Rows: [][]driver.Value{{"a", "x", "a"}, {"b", "x", "b"}, {"c", "x", "c"}}}
	})
	l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "widgets", []string{"id"},
		func(id *string) []any { return []any{id} })
	req := listing.Request{PageSize: 2}
	load := func(_ context.Context, ids []string) ([]string, error) { return ids, nil }

	_, err := sqlbase.ListHydrated(t.Context(), l, "widgets", listing.Request{PageSize: -1}, nil, load, false)
	require.Error(t, err)
	require.Empty(t, fake.Calls())

	page, err := sqlbase.ListHydrated(t.Context(), l, "widgets", req, nil,
		func(_ context.Context, ids []string) ([]string, error) { return ids[:1], nil }, false)
	require.ErrorIs(t, err, sqlbase.ErrCorruptRecord)
	require.Empty(t, page.NextPageToken)

	page, err = sqlbase.ListHydrated(t.Context(), l, "widgets", req, nil, load, false)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, page.Items)
	require.NotEmpty(t, page.NextPageToken)

	_, err = sqlbase.ListHydrated(t.Context(), l, "widgets", req, nil, load, true, sqlbase.WithCountLimit(7))
	require.ErrorIs(t, err, countErr)

	calls := fake.Calls()
	require.Len(t, calls, 4)
	require.Contains(t, calls[3].Query, "LIMIT 8")
}

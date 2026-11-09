// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func TestIntegerTiebreakColumnAndPrecision(t *testing.T) {
	t.Parallel()

	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		t.Run(map[sqlbase.ListDialect]string{postgres.ListDialect{}: "postgres", mariadb.ListDialect{}: "mariadb"}[dialect], func(t *testing.T) {
			t.Parallel()

			type item struct {
				ID int64
				At time.Time
			}

			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			var count int

			db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				count++
				if count == 1 {
					return sqlbasetest.Reply{Columns: []string{"sequence", "at", "sequence", "at"}, Rows: [][]driver.Value{{int64(2), at, int64(2), at}, {int64(10), at, int64(10), at}}}
				}

				return sqlbasetest.Reply{Columns: []string{"sequence", "at", "sequence", "at"}}
			})
			schema, err := listing.NewSchema([]listing.Field{{Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"at"}, nil)
			require.NoError(t, err)
			list, err := sqlbase.NewListEngine(db, pagetokentest.Codec(t), schema, "events", []string{"sequence", "at"}, func(it *item) []any { return []any{&it.ID, &it.At} }, dialect,
				sqlbase.WithListIDColumn("sequence"), sqlbase.WithListIDKind(filter.FieldKindInt), sqlbase.WithListTimestampPrecision(time.Second))
			require.NoError(t, err)
			first, err := list.List(t.Context(), "events", listing.Request{PageSize: 1}, nil)
			require.NoError(t, err)
			require.NotEmpty(t, first.NextPageToken)
			_, err = list.List(t.Context(), "events", listing.Request{PageSize: 1, PageToken: first.NextPageToken}, nil)
			require.NoError(t, err)
			require.Equal(t, int64(2), fake.Calls()[1].Args[len(fake.Calls()[1].Args)-1])
			require.Equal(t, at, fake.Calls()[1].Args[0])
		})
	}
}

func TestCursorRejectsUnalignedTimestamp(t *testing.T) {
	t.Parallel()

	type item struct {
		ID string
		At time.Time
	}

	at := time.Date(2026, 1, 1, 0, 0, 0, 1_000_000, time.UTC)
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "at", "id", "at"}, Rows: [][]driver.Value{{"a", at, "a", at}}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"at"}, nil)
	require.NoError(t, err)
	list, err := sqlbase.NewListEngine(db, pagetokentest.Codec(t), schema, "events", []string{"id", "at"}, func(it *item) []any { return []any{&it.ID, &it.At} }, postgres.ListDialect{}, sqlbase.WithListTimestampPrecision(time.Second))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "events", listing.Request{}, nil)
	require.ErrorIs(t, err, sqlbase.ErrCorruptRecord)
	require.ErrorIs(t, apierror.HiddenCause(err), pagetoken.ErrUnsortableValue)
}

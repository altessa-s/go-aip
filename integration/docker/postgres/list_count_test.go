// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
	"github.com/altessa-s/go-aip/softdelete"
)

func countStore(t testing.TB) *sql.DB {
	t.Helper()
	db := dbtest.Postgres17(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE books (
        id text COLLATE "C" PRIMARY KEY, tenant text COLLATE "C" NOT NULL, parent text COLLATE "C" NOT NULL,
        title text COLLATE "C" NOT NULL, deleted_at timestamptz(3));
        INSERT INTO books VALUES ('a','t1','p1','ЁЖ',NULL),('b','t1','p1','ёжик',NULL),
        ('c','t1','p1','Ёж',now()),('d','t2','p1','ёж',NULL),('e','t1','p2','ёж',NULL),('f','t1','p1','друг',NULL)`)
	require.NoError(t, err)

	return db
}

func countBooks(t testing.TB, q sqlbase.Querier) *sqlbase.ListEngine[string] {
	t.Helper()

	s := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "title", Kind: filter.FieldKindString},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}, []string{"id"}, listing.NewProfile(listing.WithCaseInsensitiveContainsFields("title")))
	l, err := postgres.NewListEngine(q, pagetokentest.Codec(t), s, "books", []string{"id"}, func(id *string) []any { return []any{id} },
		sqlbase.WithListScopeKeys("tenant", "parent"), sqlbase.WithListParentKey("parent"), sqlbase.WithListColumns(map[string]string{"deleteTime": "deleted_at"}))
	require.NoError(t, err)

	return l
}

func TestCountScopedPages(t *testing.T) {
	t.Parallel()
	db := countStore(t)
	l := countBooks(t, db)
	require.NoError(t, l.CheckSchema(t.Context(), db))

	for _, tc := range []struct {
		tenant, parent string
		showDeleted    bool
		want           []string
	}{
		{"t1", "p1", false, []string{"a", "b"}}, {"t1", "p1", true, []string{"a", "b", "c"}},
		{"t2", "p1", true, []string{"d"}}, {"t1", "p2", false, []string{"e"}}, {"t2", "p2", true, []string{}},
	} {
		scope := sqlbase.Scope{"tenant": tc.tenant, "parent": tc.parent}
		req := listing.Request{PageSize: 1, Parent: tc.parent, Filter: `title.contains("ёж")`, Restriction: softdelete.New().Restriction(tc.showDeleted)}
		ids := []string{}

		for {
			p, err := l.List(t.Context(), "books", req, scope)
			require.NoError(t, err)
			n, err := l.Count(t.Context(), req, scope)
			require.NoError(t, err)
			require.Equal(t, int64(len(tc.want)), n)

			ids = append(ids, p.Items...)
			require.LessOrEqual(t, len(ids), len(tc.want))

			if p.NextPageToken == "" {
				break
			}

			require.NotEmpty(t, p.Items)
			req.PageToken = p.NextPageToken
		}

		require.Equal(t, tc.want, ids)
	}
}

func TestCountSnapshot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		level sql.IsolationLevel
		want  int64
	}{{sql.LevelReadCommitted, 3}, {sql.LevelRepeatableRead, 2}} {
		t.Run(tc.level.String(), func(t *testing.T) {
			t.Parallel()
			db := countStore(t)
			tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: tc.level, ReadOnly: true})
			require.NoError(t, err)

			defer func() { _ = tx.Rollback() }()

			l := countBooks(t, tx)
			scope := sqlbase.Scope{"tenant": "t1", "parent": "p1"}
			req := listing.Request{Parent: "p1", Filter: `title.contains("ёж")`, Restriction: softdelete.New().Restriction(false)}
			p, err := l.List(t.Context(), "books", req, scope)
			require.NoError(t, err)
			require.Equal(t, []string{"a", "b"}, p.Items)
			_, err = db.ExecContext(t.Context(), `INSERT INTO books VALUES ('g','t1','p1','Ёж',NULL)`)
			require.NoError(t, err)
			n, err := l.Count(t.Context(), req, scope)
			require.NoError(t, err)
			require.Equal(t, tc.want, n)
			require.NoError(t, tx.Commit())
		})
	}
}

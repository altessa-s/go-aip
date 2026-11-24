// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
	"github.com/altessa-s/go-aip/softdelete"
)

func TestCountUsesCollectionPredicate(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "visible", Kind: filter.FieldKindBool},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}, []string{"id"}, listing.NewProfile(listing.WithDefaultEqualities(map[string]any{"visible": true})))

	for _, tc := range []struct {
		tenant      string
		showDeleted bool
	}{
		{"alpha", false},
		{"bravo", true},
	} {
		t.Run(tc.tenant, func(t *testing.T) {
			t.Parallel()

			db, fake := sqlbasetest.NewFakeDB(t, func(query string, args []any) sqlbasetest.Reply {
				switch {
				case strings.HasPrefix(query, "SELECT COUNT(*)"):
					return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(2)}}}
				case len(args) == 4:
					return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}, {"b", "b"}}}
				default:
					return sqlbasetest.Reply{Columns: []string{"id", "id"}}
				}
			})

			engine := postgres.MustNewListEngine(db, pagetokentest.Codec(t), schema, "app.items", []string{"id"},
				func(id *string) []any { return []any{id} },
				sqlbase.WithListScopeKeys("tenant", "parent"), sqlbase.WithListParentKey("parent"),
				sqlbase.WithListTableAlias("i"), sqlbase.WithListColumns(map[string]string{
					"parent": "parent_id", "tenant": "tenant_id", "deleteTime": "deleted_at", "name": "item_name",
				}))

			req := listing.Request{PageSize: 1, Parent: "p", Filter: `name != "other"`, Restriction: softdelete.New().Restriction(tc.showDeleted)}
			scope := sqlbase.Scope{"tenant": tc.tenant, "parent": "p"}
			page, err := engine.List(t.Context(), "items", req, scope)
			require.NoError(t, err)
			require.NotEmpty(t, page.NextPageToken)

			first := fake.Calls()[0]
			_, from, ok := strings.Cut(first.Query, " FROM ")
			require.True(t, ok)
			from, _, ok = strings.Cut(from, " ORDER BY ")
			require.True(t, ok)
			require.Contains(t, from, `"app"."items" AS "i" WHERE`)
			require.Contains(t, from, `"parent_id" = $3`)
			require.Contains(t, from, `"tenant_id" = $4`)
			require.Contains(t, from, `"item_name"`)
			require.Contains(t, from, `"visible"`)
			require.Equal(t, !tc.showDeleted, strings.Contains(from, `"deleted_at"`))
			require.Equal(t, []any{"other", true, "p", tc.tenant}, first.Args)

			for _, token := range []string{"", page.NextPageToken, "malformed"} {
				req.PageToken = token
				total, err := engine.Count(t.Context(), req, scope)
				require.NoError(t, err)
				require.EqualValues(t, 2, total)

				calls := fake.Calls()
				require.Equal(t, sqlbasetest.Call{Query: "SELECT COUNT(*) FROM " + from, Args: first.Args}, calls[len(calls)-1])
			}

			req.PageToken = page.NextPageToken
			last, err := engine.List(t.Context(), "items", req, scope)
			require.NoError(t, err)
			require.Empty(t, last.Items)

			req.PageToken = "malformed"
			_, err = engine.List(t.Context(), "items", req, scope)
			require.Error(t, err)
			require.Equal(t, pagetoken.TokenField, listingtest.FieldOf(err))
			require.Len(t, fake.Calls(), 5)
			require.Equal(t, 5, fake.RowsClosed())
		})
	}
}

func TestCountValidatesBeforeQuery(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithParentRequired(true)))
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	engine := postgres.MustNewListEngine(db, nil, schema, "items", []string{"id"}, func(id *string) []any { return []any{id} },
		sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant"))
	scope := sqlbase.Scope{"tenant": "a"}

	for _, tc := range []struct {
		name  string
		req   listing.Request
		field string
		cause error
	}{
		{"size", listing.Request{Parent: "a", PageSize: -1}, pagetoken.SizeField, nil},
		{"parent", listing.Request{}, listing.FieldParent, nil},
		{"order", listing.Request{Parent: "a", OrderBy: "unknown"}, listing.FieldOrderBy, nil},
		{"mask", listing.Request{Parent: "a", ReadMask: []string{"other"}}, listing.FieldOrderBy, nil},
		{"filter", listing.Request{Parent: "a", Filter: `unknown == 42`}, listing.FieldFilter, nil},
		{"restriction", listing.Request{Parent: "a", Restriction: &filter.IdentNode{Name: "unknown"}}, "", listing.ErrInvalidRestriction},
		{"scope", listing.Request{Parent: "b"}, "", sqlbase.ErrScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := engine.Count(t.Context(), tc.req, scope)
			require.Error(t, err)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			} else {
				require.Equal(t, tc.field, listingtest.FieldOf(err))
			}

			require.Empty(t, fake.Calls())
		})
	}
}

func TestCountAggregate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		rows [][]driver.Value
		want int64
		ok   bool
	}{
		{"empty collection", [][]driver.Value{{int64(0)}}, 0, true},
		{"wide count", [][]driver.Value{{int64(1) << 32}}, int64(1) << 32, true},
		{"missing", nil, 0, false},
		{"extra", [][]driver.Value{{int64(1)}, {int64(2)}}, 0, false},
		{"negative", [][]driver.Value{{int64(-1)}}, 0, false},
		{"invalid", [][]driver.Value{{"invalid"}}, 0, false},
		{"overflow", [][]driver.Value{{"9223372036854775808"}}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				return sqlbasetest.Reply{Columns: []string{"count"}, Rows: tc.rows}
			})
			engine := newCountEngine(t, db)

			got, err := engine.Count(t.Context(), listing.Request{}, nil)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, sqlbase.ErrCorruptRecord)
			}

			require.Equal(t, tc.want, got)
			require.Equal(t, `SELECT COUNT(*) FROM "items" AS "_listing" WHERE TRUE`, fake.Calls()[0].Query)
			require.Equal(t, 1, fake.RowsClosed())
		})
	}
}

func newCountEngine(t testing.TB, q sqlbase.Querier, opts ...sqlbase.ListEngineOption) *sqlbase.ListEngine[string] {
	t.Helper()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)

	return postgres.MustNewListEngine(q, nil, schema, "items", []string{"id"}, func(id *string) []any { return []any{id} }, opts...)
}

func TestCountExecutionErrors(t *testing.T) {
	t.Parallel()

	engine := newCountEngine(t, serverCancelQuerier{}, sqlbase.WithListMaxTime(time.Nanosecond))
	_, err := engine.Count(t.Context(), listing.Request{Filter: `id != "x"`}, nil)
	require.ErrorIs(t, err, sqlbase.ErrTimeout)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = engine.Count(ctx, listing.Request{}, nil)
	require.ErrorIs(t, err, context.Canceled)

	failure := errors.New("count query failed")
	mapped := errors.New("mapped")
	engine = newCountEngine(t, failingQuerier{failure}, sqlbase.WithListErrorMapper(func(_ context.Context, err error, filtered bool) error {
		require.ErrorIs(t, err, failure)
		require.True(t, filtered)

		return mapped
	}))
	_, err = engine.Count(t.Context(), listing.Request{Filter: `id != "x"`}, nil)
	require.ErrorIs(t, err, mapped)
}

// countIterationConnector returns the expected aggregate row, followed by a storage failure.
type countIterationConnector struct {
	driver.Connector
	fail error
}

func (c countIterationConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}

	return countIterationConn{Conn: conn, fail: c.fail}, nil
}

type countIterationConn struct {
	driver.Conn
	fail error
}

func (c countIterationConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &countIterationRows{fail: c.fail}, nil
}

type countIterationRows struct {
	seen bool
	fail error
}

func (*countIterationRows) Columns() []string { return []string{"count"} }
func (*countIterationRows) Close() error      { return nil }
func (r *countIterationRows) Next(dest []driver.Value) error {
	if r.seen {
		return r.fail
	}

	dest[0] = int64(42)
	r.seen = true

	return nil
}

func TestCountIterationError(t *testing.T) {
	t.Parallel()

	failure := errors.New("iteration failed")
	_, fake := sqlbasetest.NewFakeDB(t, nil)
	db := sql.OpenDB(countIterationConnector{fake, failure})

	t.Cleanup(func() { _ = db.Close() })
	total, err := newCountEngine(t, db).Count(t.Context(), listing.Request{}, nil)
	require.ErrorIs(t, err, failure)
	require.Zero(t, total)
}

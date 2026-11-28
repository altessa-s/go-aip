// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestBoundedCount counts at most limit+1 rows of the collection predicate and falls back to the planner estimate
// above the limit.
func TestBoundedCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		counted, plan   int64
		opts            []sqlbase.CountOption
		limitSQL        string
		want            sqlbase.Total
		wantExplanation bool
	}{
		{name: "exact", counted: 3, limitSQL: "LIMIT 1001)", want: sqlbase.Total{Count: 3, Exact: true}},
		{name: "at the limit", counted: 2, opts: []sqlbase.CountOption{sqlbase.WithCountLimit(2)}, limitSQL: "LIMIT 3)",
			want: sqlbase.Total{Count: 2, Exact: true}},
		{name: "estimate", counted: 3, plan: 1234, opts: []sqlbase.CountOption{sqlbase.WithCountLimit(2)}, limitSQL: "LIMIT 3)",
			want: sqlbase.Total{Count: 1234}, wantExplanation: true},
		{name: "low estimate", counted: 3, plan: 1, opts: []sqlbase.CountOption{sqlbase.WithCountLimit(2)}, limitSQL: "LIMIT 3)",
			want: sqlbase.Total{Count: 3}, wantExplanation: true},
		{name: "zero limit", counted: 0, opts: []sqlbase.CountOption{sqlbase.WithCountLimit(0)}, limitSQL: "LIMIT 1)",
			want: sqlbase.Total{Exact: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
				if strings.HasPrefix(query, "EXPLAIN") {
					plan := `[{"Plan": {"Plan Rows": ` + strconv.FormatInt(tc.plan, 10) + `}}]`
					return sqlbasetest.Reply{Columns: []string{"plan"}, Rows: [][]driver.Value{{[]byte(plan)}}}
				}

				return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{tc.counted}}}
			})
			l := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "app.items", []string{"id"},
				func(id *string) []any { return []any{id} }, sqlbase.WithListScopeKeys("tenant"))

			got, err := l.BoundedCount(t.Context(), listing.Request{Filter: `name != "x"`, PageToken: "ignored"}, sqlbase.Scope{"tenant": "a"}, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			calls := fake.Calls()
			require.Equal(t, `SELECT COUNT(*) FROM (SELECT 1 FROM "app"."items" AS "_listing" WHERE (("name" <> ($1)::text)) AND ("tenant" = $2) `+
				tc.limitSQL+` AS "_count"`, calls[0].Query)
			require.Equal(t, []any{"x", "a"}, calls[0].Args)

			if !tc.wantExplanation {
				require.Len(t, calls, 1)
				return
			}

			require.Len(t, calls, 2)
			require.Equal(t, `EXPLAIN (FORMAT JSON) SELECT 1 FROM "app"."items" AS "_listing" WHERE (("name" <> ($1)::text)) AND ("tenant" = $2)`,
				calls[1].Query)
			require.Equal(t, []any{"x", "a"}, calls[1].Args)
		})
	}
}

// TestBoundedCountRefusals validates the request and the count options before any statement runs.
func TestBoundedCountRefusals(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)
	dest := func(id *string) []any { return []any{id} }
	pg := postgres.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest)
	maria := sqlbase.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"}, dest, mariadb.ListDialect{})

	_, err := pg.BoundedCount(t.Context(), listing.Request{Filter: `missing == 1`}, nil)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))

	_, err = pg.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountLimit(-1))
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema)

	_, err = maria.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountProbeTime(time.Millisecond))
	require.ErrorIs(t, err, sqlbase.ErrInvalidSchema, "MariaDB cannot bound the probe")

	require.Empty(t, fake.Calls())
}

// TestBoundedCountWithoutEstimator reports the counted rows as a lower bound on a dialect that does not estimate.
func TestBoundedCountWithoutEstimator(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(6)}}}
	})
	l := sqlbase.MustNewListEngine(db, pagetokentest.Codec(t), countSchema(t), "items", []string{"id"},
		func(id *string) []any { return []any{id} }, mariadb.ListDialect{})

	got, err := l.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountLimit(5))
	require.NoError(t, err)
	require.Equal(t, sqlbase.Total{Count: 6}, got)
	require.Equal(t, "SELECT COUNT(*) FROM (SELECT 1 FROM `items` AS `_listing` WHERE TRUE LIMIT 6) AS `_count`", fake.Calls()[0].Query)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
)

// stateError is a driver error reporting its SQLSTATE as pgx and lib/pq errors do.
type stateError string

func (e stateError) Error() string    { return "SQLSTATE " + string(e) }
func (e stateError) SQLState() string { return string(e) }

const probeQuery = "SELECT COUNT(*) FROM (SELECT 1 FROM t LIMIT 3) AS c"

func countReply(n int64) sqlbasetest.Reply {
	return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{n}}}
}

// TestProbeCountInTransaction scopes the statement timeout to a savepoint and reports a canceled probe as cut short.
func TestProbeCountInTransaction(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		reply  sqlbasetest.Reply
		count  int64
		done   bool
		failed bool
	}{
		{name: "counted", reply: countReply(2), count: 2, done: true},
		{name: "timed out", reply: sqlbasetest.Reply{Err: stateError("57014")}},
		{name: "failed", reply: sqlbasetest.Reply{Err: stateError("42P01")}, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
				if strings.HasPrefix(query, "SELECT") {
					return tc.reply
				}

				return sqlbasetest.Reply{}
			})
			tx, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)

			defer func() { _ = tx.Rollback() }()

			count, done, err := postgres.ListDialect{}.ProbeCount(t.Context(), tx, probeQuery, []any{"x"}, 1500*time.Microsecond)
			require.Equal(t, tc.failed, err != nil, "%v", err)
			require.Equal(t, tc.count, count)
			require.Equal(t, tc.done, done)

			var statements []string

			for _, call := range fake.Calls() {
				require.True(t, call.InTx)
				statements = append(statements, call.Query)
			}

			require.Equal(t, []string{
				"SAVEPOINT aip_count_probe", "SET LOCAL statement_timeout = '2ms'", probeQuery,
				"ROLLBACK TO SAVEPOINT aip_count_probe", "RELEASE SAVEPOINT aip_count_probe",
			}, statements)
			require.Equal(t, []any{"x"}, fake.Calls()[2].Args)
		})
	}
}

// TestProbeCountOutsideTransaction bounds the probe on a pool with a deadline and sends no savepoint.
func TestProbeCountOutsideTransaction(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.HasPrefix(query, "SAVEPOINT") {
			return sqlbasetest.Reply{Err: stateError("25P01")}
		}

		return countReply(4)
	})

	count, done, err := postgres.ListDialect{}.ProbeCount(t.Context(), db, probeQuery, nil, time.Second)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, int64(4), count)
	require.Len(t, fake.Calls(), 1)
	require.Equal(t, probeQuery, fake.Calls()[0].Query)
	require.False(t, fake.Calls()[0].InTx)

	// A connection outside a transaction learns it from the refused savepoint.
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	count, done, err = postgres.ListDialect{}.ProbeCount(t.Context(), conn, probeQuery, nil, time.Second)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, int64(4), count)
	require.Len(t, fake.Calls(), 3)
	require.Equal(t, "SAVEPOINT aip_count_probe", fake.Calls()[1].Query)

	slow, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Err: stateError("57014")} })
	count, done, err = postgres.ListDialect{}.ProbeCount(t.Context(), slow, probeQuery, nil, time.Second)
	require.NoError(t, err, "a canceled probe on a pool is cut short")
	require.False(t, done)
	require.Zero(t, count)

	db, _ = sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Err: errors.New("down")} })
	_, _, err = postgres.ListDialect{}.ProbeCount(t.Context(), db, probeQuery, nil, time.Second)
	require.ErrorContains(t, err, "down")
}

// TestEstimateCount reads the planner's row estimate from the JSON plan.
func TestEstimateCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		plan    string
		want    int64
		invalid bool
	}{
		{plan: `[{"Plan": {"Node Type": "Seq Scan", "Plan Rows": 1234}}]`, want: 1234},
		{plan: `[{"Plan": {"Plan Rows": 12.2}}]`, want: 13},
		{plan: `[]`, invalid: true},
		{plan: `{`, invalid: true},
	} {
		db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
			return sqlbasetest.Reply{Columns: []string{"QUERY PLAN"}, Rows: [][]driver.Value{{[]byte(tc.plan)}}}
		})

		got, err := postgres.ListDialect{}.EstimateCount(t.Context(), db, "SELECT 1 FROM t WHERE a = $1", []any{1})
		require.Equal(t, tc.invalid, err != nil, tc.plan)
		require.Equal(t, tc.want, got, tc.plan)
		require.Equal(t, "EXPLAIN (FORMAT JSON) SELECT 1 FROM t WHERE a = $1", fake.Calls()[0].Query)
		require.Equal(t, []any{int64(1)}, fake.Calls()[0].Args)
	}
}

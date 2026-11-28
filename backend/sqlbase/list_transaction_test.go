// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/listing"
)

type pausedProbeDialect struct {
	postgres.ListDialect
	resume <-chan struct{}
}

func (d pausedProbeDialect) ProbeCount(ctx context.Context, q sqlbase.Querier, query string, args []any, maxTime time.Duration) (int64, bool, error) {
	return d.ListDialect.ProbeCount(ctx, pausedProbeQuerier{Querier: q, resume: d.resume}, query, args, maxTime)
}

type pausedProbeQuerier struct {
	sqlbase.Querier
	resume <-chan struct{}
}

func (q pausedProbeQuerier) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := q.Querier.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}).ExecContext(ctx, query, args...)
	if err == nil && strings.HasPrefix(query, "SAVEPOINT ") {
		<-q.resume
	}

	return result, err
}

func TestListTransactionSerializesProbe(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "one engine"
		if shared {
			name = "shared coordinator"
		}

		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				testListTransactionSerializesProbe(t, shared)
			})
		})
	}
}

func testListTransactionSerializesProbe(t *testing.T, shared bool) {
	t.Helper()

	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.HasPrefix(query, "SELECT COUNT(*)") {
			return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(1)}}}
		}

		return sqlbasetest.Reply{}
	})
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	resume := make(chan struct{})
	dest := func(id *string) []any { return []any{id} }
	dialect := pausedProbeDialect{resume: resume}

	var (
		coordinator *sqlbase.ListTransaction
		opts        []sqlbase.ListEngineOption
	)

	if shared {
		coordinator = sqlbase.NewListTransaction(tx)
		opts = append(opts, sqlbase.WithListTransaction(coordinator))
	}

	l := sqlbase.MustNewListEngine(tx, nil, countSchema(t), "items", []string{"id"}, dest, dialect, opts...)

	reader := l
	if shared {
		reader = postgres.MustNewListEngine(nil, nil, countSchema(t), "items", []string{"id"}, dest, opts...)
	}

	ctx := t.Context()
	req := listing.Request{}

	probeErr := make(chan error, 1)

	go func() {
		_, err := l.BoundedCount(ctx, req, nil, sqlbase.WithCountProbeTime(time.Second))
		probeErr <- err
	}()

	synctest.Wait()

	listErr := make(chan error, 1)

	go func() {
		_, err := reader.List(ctx, "items", req, nil)
		listErr <- err
	}()

	countErr := make(chan error, 1)

	go func() {
		_, err := reader.Count(ctx, req, nil)
		countErr <- err
	}()

	execErr := make(chan error, 1)

	if shared {
		go func() {
			execErr <- coordinator.Run(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "UPDATE items SET name = 'changed'")
				return err
			})
		}()

		waitCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
		err := coordinator.Run(waitCtx, func(*sql.Tx) error {
			t.Error("operation ran before probe cleanup")
			return nil
		})

		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}

	synctest.Wait()

	duringProbe := fake.Calls()

	close(resume)
	require.NoError(t, <-probeErr)
	require.NoError(t, <-listErr)
	require.NoError(t, <-countErr)
	require.Equal(t, []sqlbasetest.Call{{Query: "SAVEPOINT aip_count_probe", Args: []any{}, InTx: true}}, duringProbe)

	wantCalls := 7

	if shared {
		require.NoError(t, <-execErr)

		wantCalls++
	}

	calls := fake.Calls()
	require.Len(t, calls, wantCalls)
	require.Equal(t, "ROLLBACK TO SAVEPOINT aip_count_probe", calls[3].Query)
	require.Equal(t, "RELEASE SAVEPOINT aip_count_probe", calls[4].Query)
}

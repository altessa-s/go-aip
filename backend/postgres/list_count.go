// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

// SQLSTATE codes of the count probe.
const (
	queryCanceled       = "57014"
	noActiveTransaction = "25P01"
)

// countProbe is the savepoint that scopes the statement timeout of a count probe.
const countProbe = "aip_count_probe"

const countProbeCleanupTimeout = 2 * time.Second

// ProbeCount runs query in a savepoint under a statement timeout of maxTime, rounded up to a millisecond. Rolling the
// savepoint back uses a separate deadline and restores the previous timeout if the driver keeps the connection open.
// On a *sql.DB, which runs each statement outside a transaction, a context deadline bounds the probe instead and no
// savepoint is sent. Another querier that turns out to be outside a transaction gets the deadline once PostgreSQL
// refuses the savepoint (25P01). A driver reports SQLSTATE through an error with a SQLState() string method, as pgx and
// lib/pq errors do.
func (ListDialect) ProbeCount(ctx context.Context, q sqlbase.Querier, query string, args []any, maxTime time.Duration) (
	count int64, done bool, err error,
) {
	if _, pool := q.(*sql.DB); pool {
		return probeWithDeadline(ctx, q, query, args, maxTime)
	}

	if err = exec(ctx, q, "SAVEPOINT "+countProbe); err != nil {
		if sqlState(err) == noActiveTransaction {
			return probeWithDeadline(ctx, q, query, args, maxTime)
		}

		return 0, false, err
	}

	defer func() {
		if ctxErr := ctx.Err(); err != nil && ctxErr != nil && !errors.Is(err, ctxErr) {
			err = errors.Join(ctxErr, err)
		}

		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), countProbeCleanupTimeout)
		defer cancel()

		cleanupErr := exec(cleanupCtx, q, "ROLLBACK TO SAVEPOINT "+countProbe)
		if cleanupErr == nil {
			cleanupErr = exec(cleanupCtx, q, "RELEASE SAVEPOINT "+countProbe)
		}

		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}

		if err != nil {
			count, done = 0, false
		}
	}()

	ms := maxTime / time.Millisecond
	if maxTime%time.Millisecond > 0 {
		ms++
	}

	ms = max(1, min(ms, math.MaxInt32))
	if err = exec(ctx, q, "SET LOCAL statement_timeout = '"+strconv.FormatInt(int64(ms), 10)+"ms'"); err != nil {
		return 0, false, err
	}

	count, err = queryCount(ctx, q, query, args)
	if sqlState(err) == queryCanceled && ctx.Err() == nil {
		return 0, false, nil
	}

	return count, err == nil, err
}

// EstimateCount returns the planner's row estimate of query from EXPLAIN (FORMAT JSON).
func (ListDialect) EstimateCount(ctx context.Context, q sqlbase.Querier, query string, args []any) (int64, error) {
	rows, err := q.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+query, args...)
	if err != nil {
		return 0, err
	}

	defer func() { _ = rows.Close() }()

	var raw []byte

	if !rows.Next() {
		return 0, errors.Join(errors.New("missing query plan"), rows.Err())
	}

	if err = rows.Scan(&raw); err != nil {
		return 0, err
	}

	if err = rows.Close(); err != nil {
		return 0, err
	}

	var plans []struct {
		Plan struct {
			Rows float64 `json:"Plan Rows"`
		}
	}

	if err = json.Unmarshal(raw, &plans); err != nil {
		return 0, fmt.Errorf("decode query plan: %w", err)
	}

	if len(plans) != 1 {
		return 0, fmt.Errorf("decode query plan: %d plans", len(plans))
	}

	estimate := math.Ceil(plans[0].Plan.Rows)
	switch {
	case estimate >= math.MaxInt64:
		return math.MaxInt64, nil
	case estimate <= 0:
		return 0, nil
	default:
		return int64(estimate), nil
	}
}

// probeWithDeadline runs query outside a transaction, where a canceled statement harms only its own connection.
func probeWithDeadline(ctx context.Context, q sqlbase.Querier, query string, args []any, maxTime time.Duration) (int64, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, maxTime)
	defer cancel()

	count, err := queryCount(probeCtx, q, query, args)
	if err != nil && ctx.Err() == nil && (probeCtx.Err() != nil || sqlState(err) == queryCanceled) {
		return 0, false, nil
	}

	return count, err == nil, err
}

func queryCount(ctx context.Context, q sqlbase.Querier, query string, args []any) (int64, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	defer func() { _ = rows.Close() }()

	var count int64

	if !rows.Next() {
		return 0, errors.Join(errors.New("missing count row"), rows.Err())
	}

	if err = rows.Scan(&count); err != nil {
		return 0, err
	}

	return count, rows.Close()
}

// exec runs a statement without arguments, through ExecContext when the querier has it.
func exec(ctx context.Context, q sqlbase.Querier, statement string) error {
	if e, ok := q.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}); ok {
		_, err := e.ExecContext(ctx, statement)
		return err
	}

	rows, err := q.QueryContext(ctx, statement)
	if err != nil {
		return err
	}

	return errors.Join(rows.Close(), rows.Err())
}

func sqlState(err error) string {
	if e, ok := errors.AsType[interface {
		error
		SQLState() string
	}](err); ok {
		return e.SQLState()
	}

	return ""
}

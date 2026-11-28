// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestBoundedCount counts exactly up to the limit, estimates above it and, when the probe expires inside a
// transaction, estimates without aborting the transaction.
func TestBoundedCount(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	_, err := db.ExecContext(t.Context(), `ANALYZE assets`)
	require.NoError(t, err)

	l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), assetSchema(t, "createTime"), "assets", []string{"id"}, idDest,
		assetOptions()...)
	require.NoError(t, err)

	got, err := l.BoundedCount(t.Context(), listing.Request{Filter: `kind == 1`}, nil)
	require.NoError(t, err)
	require.Equal(t, sqlbase.Total{Count: 8, Exact: true}, got)

	got, err = l.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountLimit(10))
	require.NoError(t, err)
	require.False(t, got.Exact)
	require.GreaterOrEqual(t, got.Count, int64(11))

	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	slow := sqlbase.MustNewListEngine(tx, pagetokentest.Codec(t), assetSchema(t, "createTime"), "assets", []string{"id"}, idDest,
		postgres.ListDialect{}, append(assetOptions(), sqlbase.WithListTableExpression(`SELECT * FROM assets WHERE pg_sleep(0.01) IS NOT NULL`))...)

	got, err = slow.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountProbeTime(50*time.Millisecond))
	require.NoError(t, err)
	require.False(t, got.Exact, "the probe of 40 rows sleeping 10ms each expires")

	var timeout string
	require.NoError(t, tx.QueryRowContext(t.Context(), `SHOW statement_timeout`).Scan(&timeout))
	require.Equal(t, "0", timeout, "the savepoint restored the timeout and the transaction is usable")

	got, err = slow.BoundedCount(t.Context(), listing.Request{Filter: `kind == 1`}, nil, sqlbase.WithCountProbeTime(5*time.Second))
	require.NoError(t, err)
	require.Equal(t, sqlbase.Total{Count: 8, Exact: true}, got)

	pooled := sqlbase.MustNewListEngine(db, pagetokentest.Codec(t), assetSchema(t, "createTime"), "assets", []string{"id"}, idDest,
		postgres.ListDialect{}, append(assetOptions(), sqlbase.WithListTableExpression(`SELECT * FROM assets WHERE pg_sleep(0.01) IS NOT NULL`))...)

	got, err = pooled.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountProbeTime(50*time.Millisecond))
	require.NoError(t, err)
	require.False(t, got.Exact, "a deadline bounds the probe outside a transaction")

	var one int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one), "the pool still serves")
}

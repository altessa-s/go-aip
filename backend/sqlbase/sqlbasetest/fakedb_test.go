// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbasetest_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
)

func TestFakeDBQueryAndExec(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		switch query {
		case "SELECT":
			return sqlbasetest.Reply{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{int64(1), "a"}, {int64(2), "b"}}}
		case "FAIL":
			return sqlbasetest.Reply{Err: boom}
		default:
			return sqlbasetest.Reply{Affected: 3}
		}
	})

	rows, err := db.QueryContext(t.Context(), "SELECT", "x", 7)
	require.NoError(t, err)

	var got []string

	for rows.Next() {
		var (
			id   int64
			name string
		)

		require.NoError(t, rows.Scan(&id, &name))

		got = append(got, name)
	}

	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Equal(t, []string{"a", "b"}, got)

	res, err := db.ExecContext(t.Context(), "UPDATE")
	require.NoError(t, err)

	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	_, err = db.ExecContext(t.Context(), "FAIL")
	require.ErrorIs(t, err, boom)

	_, err = db.QueryContext(t.Context(), "FAIL") //nolint:rowserrcheck,sqlclosecheck // the query fails; there are no rows
	require.ErrorIs(t, err, boom)

	calls := fake.Calls()
	require.Len(t, calls, 4)
	assert.Equal(t, sqlbasetest.Call{Query: "SELECT", Args: []any{"x", int64(7)}}, calls[0])
	assert.Equal(t, 1, fake.RowsClosed())
}

func TestFakeDBDefaultReplyAndTransactions(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)

	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	res, err := tx.ExecContext(t.Context(), "INSERT")
	require.NoError(t, err)

	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "a nil script affects one row")
	require.NoError(t, tx.Commit())

	tx, err = db.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	stmt, err := tx.PrepareContext(t.Context(), "DELETE")
	require.NoError(t, err)

	_, err = stmt.ExecContext(t.Context(), 1)
	require.NoError(t, err)
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Rollback())

	_, err = db.ExecContext(t.Context(), "AFTER")
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 3)
	assert.True(t, calls[0].InTx)
	assert.Equal(t, sqlbasetest.Call{Query: "DELETE", Args: []any{int64(1)}, InTx: true}, calls[1], "a prepared statement records each execution")
	assert.False(t, calls[2].InTx)
	assert.Equal(t, 1, fake.Commits())
	assert.Equal(t, 1, fake.Rollbacks())
}

func TestFakeDBDisableRecording(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)
	fake.DisableRecording()

	_, err := db.ExecContext(t.Context(), "INSERT")
	require.NoError(t, err)
	assert.Empty(t, fake.Calls())
}

func TestFakeDBConcurrentUse(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)

	const workers = 8

	errs := make(chan error, workers)
	for range workers {
		go func() {
			_, err := db.ExecContext(t.Context(), "INSERT")
			errs <- err
		}()
	}

	for range workers {
		require.NoError(t, <-errs)
	}

	assert.Len(t, fake.Calls(), workers)
}

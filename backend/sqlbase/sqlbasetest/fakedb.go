// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbasetest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// Reply scripts the answer of a [FakeDB] database to one statement:
// rows for a query, an affected-row count for an exec, or an error for both.
type Reply struct {
	Columns  []string
	Rows     [][]driver.Value
	Affected int64
	Err      error
}

// Call is one statement a [FakeDB] database received.
type Call struct {
	Query string
	Args  []any
	// InTx reports whether the statement ran inside a transaction.
	InTx bool
}

// FakeDB is a database/sql driver that records every statement and answers
// from a script, so SQL-building code can be tested for statement shape,
// argument order and result mapping without a server or a third-party driver.
// Prepared statements record each execution like a direct one, and
// transactions only mark the statements issued inside them.
type FakeDB struct {
	mu        sync.Mutex
	calls     []Call
	closed    int
	commits   int
	rollbacks int
	noRecord  bool
	respond   func(query string, args []any) Reply
}

// NewFakeDB returns a *sql.DB backed by a [FakeDB] and the fake itself.
// respond may be nil, in which case every statement succeeds affecting one row.
// The database is closed via tb.Cleanup.
func NewFakeDB(tb testing.TB, respond func(query string, args []any) Reply) (*sql.DB, *FakeDB) {
	tb.Helper()

	f := &FakeDB{respond: respond}
	if f.respond == nil {
		f.respond = func(string, []any) Reply { return Reply{Affected: 1} }
	}

	db := sql.OpenDB(f)

	tb.Cleanup(func() { _ = db.Close() })

	return db, f
}

// Calls returns the statements received so far, in order.
func (f *FakeDB) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]Call(nil), f.calls...)
}

// DisableRecording stops [FakeDB.Calls] from accumulating statements, so a
// benchmark issuing millions of them runs in constant memory. Statements are
// still answered by the script.
func (f *FakeDB) DisableRecording() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.noRecord = true
}

// RowsClosed returns how many result sets were closed.
func (f *FakeDB) RowsClosed() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closed
}

// Commits returns how many transactions were committed.
func (f *FakeDB) Commits() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.commits
}

// Rollbacks returns how many transactions were rolled back.
func (f *FakeDB) Rollbacks() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.rollbacks
}

func (f *FakeDB) record(query string, args []driver.NamedValue, inTx bool) Reply {
	values := make([]any, len(args))
	for i, a := range args {
		values[i] = a.Value
	}

	f.mu.Lock()
	if !f.noRecord {
		f.calls = append(f.calls, Call{Query: query, Args: values, InTx: inTx})
	}

	respond := f.respond
	f.mu.Unlock()

	return respond(query, values)
}

// Connect implements driver.Connector.
func (f *FakeDB) Connect(context.Context) (driver.Conn, error) { return &fakeConn{db: f}, nil }

// Driver implements driver.Connector.
func (f *FakeDB) Driver() driver.Driver { return fakeDriver{f} }

type fakeDriver struct{ db *FakeDB }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{db: d.db}, nil }

type fakeConn struct {
	db   *FakeDB
	inTx bool
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}

func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	c.inTx = true
	return &fakeTx{conn: c}, nil
}

// BeginTx accepts any isolation level, so code that asks for one is testable.
func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.Begin() }

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	r := c.db.record(query, args, c.inTx)
	if r.Err != nil {
		return nil, r.Err
	}

	return driver.RowsAffected(r.Affected), nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r := c.db.record(query, args, c.inTx)
	if r.Err != nil {
		return nil, r.Err
	}

	return &fakeRows{db: c.db, columns: r.Columns, rows: r.Rows}, nil
}

// fakeStmt records each execution of a prepared statement like a direct one.
type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("sqlbasetest: FakeDB needs ExecContext")
}

func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("sqlbasetest: FakeDB needs QueryContext")
}

func (s *fakeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}

func (s *fakeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

type fakeTx struct{ conn *fakeConn }

func (t *fakeTx) Commit() error {
	t.conn.inTx = false
	t.conn.db.mu.Lock()
	t.conn.db.commits++
	t.conn.db.mu.Unlock()

	return nil
}

func (t *fakeTx) Rollback() error {
	t.conn.inTx = false
	t.conn.db.mu.Lock()
	t.conn.db.rollbacks++
	t.conn.db.mu.Unlock()

	return nil
}

type fakeRows struct {
	db      *FakeDB
	columns []string
	rows    [][]driver.Value
	i       int
}

func (r *fakeRows) Columns() []string { return r.columns }

func (r *fakeRows) Close() error {
	r.db.mu.Lock()
	r.db.closed++
	r.db.mu.Unlock()

	return nil
}

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}

	copy(dest, r.rows[r.i])
	r.i++

	return nil
}

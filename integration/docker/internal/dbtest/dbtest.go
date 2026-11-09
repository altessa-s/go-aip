// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// Images are the server images the helpers start.
const (
	PostgresImage   = "postgres:16"
	MariaDBImage    = "mariadb:11"
	ClickHouseImage = "clickhouse/clickhouse-server:26.8"
	MongoImage      = "mongo:8.0"
)

// startTimeout bounds the start of one server, image pull included.
const startTimeout = 5 * time.Minute

// cleanupTimeout bounds dropping one database and stopping one server.
const cleanupTimeout = time.Minute

// maxOpenConns caps the pool of one test database, so many parallel tests stay under the server's connection limit.
const maxOpenConns = 8

// engine describes one database server: how to start it and how to create, reach and drop a database on it. Adding a server
// is adding one engine and one exported helper calling [engine.open]; see README.md.
type engine struct {
	// name names the server in skip and failure messages.
	name string
	// driver is the database/sql driver name of the server's connections.
	driver string
	// start runs the server and returns it with the DSN of its administrative database.
	start func(ctx context.Context) (testcontainers.Container, string, error)
	// database returns the DSN of database on the server whose administrative DSN is admin.
	database func(admin, database string) (string, error)
	// create and drop are the statements creating and dropping a database; %s is its (unquoted, generated) name.
	create, drop string

	once  sync.Once
	ready *server
	err   error
	skip  string
}

// server is a started engine.
type server struct {
	container testcontainers.Container
	admin     *sql.DB
	dsn       string
}

// stopper is a started server [Run] stops: an SQL [engine] or the MongoDB server.
type stopper interface {
	label() string
	stop() error
}

var (
	// started lists the servers that started, for [Run] to stop.
	startedMu sync.Mutex
	started   []stopper

	// databases numbers the databases of the test binary; the servers are private to it, so the names never collide.
	databases atomic.Int64
)

// Run runs the tests of m and stops every server they started. Call it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }
func Run(m *testing.M) int {
	code := m.Run()

	startedMu.Lock()
	defer startedMu.Unlock()

	for _, e := range started {
		if err := e.stop(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "dbtest: stop %s: %v\n", e.label(), err)

			if code == 0 {
				code = 1
			}
		}
	}

	started = nil

	return code
}

// open returns a connection pool to a new database on e's server, started on first use, and registers its cleanup on tb. It
// skips tb when Docker is unreachable and fails it when the server cannot start or the database cannot be created.
func (e *engine) open(tb testing.TB) *sql.DB {
	tb.Helper()

	e.once.Do(e.boot)

	if e.skip != "" {
		tb.Skipf("dbtest: %s needs Docker: %s", e.name, e.skip)
	}

	if e.err != nil {
		tb.Fatalf("dbtest: start %s: %v", e.name, e.err)
	}

	name := fmt.Sprintf("aip_%06d", databases.Add(1))
	if _, err := e.ready.admin.ExecContext(tb.Context(), fmt.Sprintf(e.create, name)); err != nil {
		tb.Fatalf("dbtest: create %s database %s: %v", e.name, name, err)
	}

	dsn, err := e.database(e.ready.dsn, name)
	if err != nil {
		tb.Fatalf("dbtest: %s database %s: %v", e.name, name, err)
	}

	db, err := sql.Open(e.driver, dsn)
	if err != nil {
		tb.Fatalf("dbtest: open %s database %s: %v", e.name, name, err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		if err := errors.Join(db.Close(), exec(ctx, e.ready.admin, fmt.Sprintf(e.drop, name))); err != nil {
			tb.Errorf("dbtest: drop %s database %s: %v", e.name, name, err)
		}
	})

	if err := db.PingContext(tb.Context()); err != nil {
		tb.Fatalf("dbtest: connect to %s database %s: %v", e.name, name, err)
	}

	return db
}

// label names the server in messages.
func (e *engine) label() string { return e.name }

// boot starts e's server once, recording why it cannot when Docker is unreachable.
func (e *engine) boot() {
	if reason := dockerUnavailable(); reason != "" {
		e.skip = reason

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, dsn, err := e.start(ctx)
	if ctr != nil {
		startedMu.Lock()

		started = append(started, e)
		startedMu.Unlock()
	}

	e.ready = &server{container: ctr, dsn: dsn}
	if err != nil {
		e.err = err

		return
	}

	admin, err := sql.Open(e.driver, dsn)
	if err == nil {
		err = admin.PingContext(ctx)
	}

	if err != nil {
		e.err = fmt.Errorf("connect: %w", err)

		return
	}

	e.ready.admin = admin
}

// stop closes the administrative pool of e and terminates its container.
func (e *engine) stop() error {
	if e.ready == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	var errs []error
	if e.ready.admin != nil {
		errs = append(errs, e.ready.admin.Close())
	}

	if e.ready.container != nil {
		errs = append(errs, e.ready.container.Terminate(ctx))
	}

	return errors.Join(errs...)
}

// dockerUnavailable returns why no container can start, or "" when the Docker daemon answers.
func dockerUnavailable() (reason string) {
	defer func() {
		if r := recover(); r != nil {
			reason = fmt.Sprint(r)
		}
	}()

	provider, err := testcontainers.ProviderDefault.GetProvider()
	if err != nil {
		return err.Error()
	}

	defer func() { _ = provider.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	if err := provider.Health(ctx); err != nil {
		return err.Error()
	}

	return ""
}

func exec(ctx context.Context, db *sql.DB, query string) error {
	_, err := db.ExecContext(ctx, query)

	return err
}

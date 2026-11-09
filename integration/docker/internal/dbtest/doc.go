// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package dbtest starts the live database servers the integration tests run
// against and hands each test an isolated database on them. It builds only
// with the integration_docker build tag; without the tag the package is
// empty.
//
// Each server (PostgreSQL 16, MariaDB 11, ClickHouse 26.8, MongoDB 8.0) is a
// testcontainers-go container started lazily, once per test binary, by the
// first test that asks for it. [Postgres], [MariaDB] and [ClickHouse] create a
// fresh database on that server, return a *sql.DB connected to it and
// register a cleanup that closes the pool and drops the database; [Mongo]
// returns a fresh *mongo.Database of a shared client and drops it when the
// test ends, and [MongoClientOptions] the options of a client of its own. A
// test binary that uses the package calls [Run] from
// its TestMain so the containers stop when the binary ends. When Docker is
// unreachable every helper skips the test.
//
// # Usage
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }
//
//	func TestLive(t *testing.T) {
//		t.Parallel()
//		db := dbtest.Postgres(t) // a database of its own
//		_, err := db.ExecContext(t.Context(), `CREATE TABLE widgets (id text PRIMARY KEY)`)
//		require.NoError(t, err)
//	}
//
// See README.md for the servers, their settings and how to add another one.
package dbtest

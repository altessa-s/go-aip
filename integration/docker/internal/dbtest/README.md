# dbtest

```go
import "github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
```

Package `dbtest` runs the live database servers of the integration tests and gives each test an isolated database on them.
Internal to the test-only module [`integration/docker`](../../README.md). It builds only with the `integration_docker` build tag; without it the
package is empty, so a plain `go test ./...` never needs Docker.

```sh
go -C integration/docker test -race -tags integration_docker ./...             # every live suite
go -C integration/docker test -race -tags integration_docker ./internal/dbtest/ # the helpers alone
```

## Contract

| Helper                   | Returns                                                                                                                                                  |
|--------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Postgres(tb)`           | `*sql.DB` (pgx `database/sql` driver) on a new database of a PostgreSQL 16 server                                                                        |
| `MariaDB(tb)`            | `*sql.DB` (go-sql-driver/mysql) on a new database of a MariaDB 11 server                                                                                 |
| `ClickHouse(tb)`         | `*sql.DB` (clickhouse-go v2, native protocol) on a new database of a ClickHouse 26.8 LTS server                                                          |
| `Mongo(tb)`              | `*mongo.Database` (mongo-driver v2, a client with the `backend/mongo` registry) of a new database of a MongoDB 8.0 server                                |
| `MongoClientOptions(tb)` | The options of a new client of the MongoDB server (its URI and the `backend/mongo` registry), for a client of its own such as one with a command monitor |
| `Run(m)`                 | Runs the tests of `m`, then stops every server they started; call it from `TestMain`                                                                     |

- **Once per test binary.** A server is a testcontainers-go container started by the first test that asks for it (`sync.Once`) and
  shared by every test of the binary. Each test package is its own binary, so packages never share a server or interfere.
- **Isolation.** Every call creates a database of its own (`aip_000001`, …) and registers a cleanup that closes the pool and drops the
  database, so parallel tests never see each other's tables. A pool holds at most 8 connections; the servers accept at least 500. `Mongo`
  shares one client of the server and drops the database when the test ends.
- **No Docker, no failure.** When the Docker daemon does not answer, every helper calls `tb.Skip` with the reason. A server that
  cannot start while Docker answers fails the test.
- **Stopping.** `Run` terminates the containers when the binary ends; the testcontainers reaper (Ryuk) removes them if the binary is
  killed.

```go
func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

func TestLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	_, err := db.ExecContext(t.Context(), "CREATE TABLE widgets (id VARCHAR(16) PRIMARY KEY)")
	require.NoError(t, err)
}
```

## Servers

| Server     | Image                               | Settings                                                                                                                                                                                                                                          |
|------------|-------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| PostgreSQL | `postgres:16`                       | UTF-8 databases from `template0`, `timezone=UTC`, `fsync=off`, `max_connections=500`                                                                                                                                                              |
| MariaDB    | `mariadb:11`                        | databases default to `utf8mb4` / `utf8mb4_nopad_bin` (the listing storage contract), `default-time-zone=+00:00`, `max-connections=500`; DSN with `parseTime=true`, `loc=UTC`, `multiStatements=true`                                              |
| MongoDB    | `mongo:8.0`                         | a standalone node (testcontainers `mongodb` module); clients store values with `mongo.NewRegistry()` (Decimal128 decimals, optional values, dates)                                                                                                |
| ClickHouse | `clickhouse/clickhouse-server:26.8` | the LTS release; server time zone UTC; databases use the default `Atomic` engine and are dropped with `SYNC`; one statement per query; batch inserts through `BeginTx`, `PrepareContext("INSERT INTO t (…)")`, `ExecContext` per row and `Commit` |

The images are the constants `PostgresImage`, `MariaDBImage`, `ClickHouseImage` and `MongoImage`. Columns that need another collation declare it
themselves; note that a MariaDB `JSON` column defaults to `utf8mb4_bin` whatever the database default, so a listing array column spells `JSON COLLATE
utf8mb4_nopad_bin`.

## Adding a server

A server is one `engine` value in `servers.go` plus one exported helper calling `engine.open`, its image constant in `dbtest.go` and a row
in the `TestIsolation` table of `dbtest_test.go`; the ClickHouse engine is the shortest example (a testcontainers module's `Run`, a URL DSN
whose path names the database, and the `CREATE DATABASE` / `DROP DATABASE` statements). A package of the module that uses the new helper adds a
`main_test.go` with `TestMain` calling `Run`. `engine.open` does the rest: the lazy start, the Docker check, a database per test
with its cleanup, the pool limit and stopping the container from `Run`. A server without `database/sql` (MongoDB, `mongo.go`) implements the
`stopper` interface `Run` stops and its own lazy start, and keeps the same contract.

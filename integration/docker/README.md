# integration/docker

```text
module github.com/altessa-s/go-aip/integration/docker
```

Live database tests of go-aip. The PostgreSQL, MariaDB, ClickHouse and MongoDB backends run their conformance suites against real servers
started in Docker with testcontainers-go. This is a test-only nested Go module: it is never released or tagged, and it requires the root module
and the `backend/sqlbase`, `backend/postgres`, `backend/mariadb`, `backend/clickhouse` and `backend/mongo` modules through `replace` directives
to the checkout (`github.com/altessa-s/go-aip => ../..`, `github.com/altessa-s/go-aip/backend/postgres => ../../backend/postgres`, …), so
testcontainers-go, pgx, go-sql-driver/mysql and clickhouse-go stay out of the `go.mod` of every released module.

## Running

The tests build only with the `integration_docker` build tag and need a reachable Docker daemon (each test skips when Docker does not answer).

```sh
make test-integration-docker                                           # from the repository root
go -C integration/docker test -race -tags integration_docker ./...      # the same, by hand
go -C integration/docker test -race -tags integration_docker ./mariadb/ # one backend
```

Without the tag every package holds only its `doc.go`, so `go test ./...` and `go vet ./...` in this module need neither Docker nor the
servers. Lint and vet the tests with the tag: `go vet -tags integration_docker ./...`,
`golangci-lint run --build-tags=integration_docker ./...`.

## Packages

| Package           | Runs against a live server                                                                                                                                                                                                                                                                                                                                                                                                 |
|-------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `postgres`        | PostgreSQL 17: Unicode 15.1 lower parity, scoped totals, transaction snapshots and substring profiles; PostgreSQL 16: opt-in preflight rejection, filter conformance (scalar translator), contract and schema translators, NULL cross-checks, order and projection conformance, listing conformance (`listingtest.Run`), inline literals in every multibyte client encoding                                                                                                                                                                                   |
| `mariadb`         | MariaDB 11: filter conformance, contract translator, NULL cross-checks, order and projection conformance, listing conformance, inline literals in every multibyte connection character set                                                                                                                                                                                                                                 |
| `clickhouse`      | ClickHouse 26.8 LTS on MergeTree tables: filter conformance, contract and schema translators, NULL cross-checks, order and projection conformance, listing conformance, inline non-ASCII literals                                                                                                                                                                                                                          |
| `mongo`           | MongoDB 8.0: filter conformance (each query executed by the server, compared with the evaluator under the MongoDB profile), listing conformance (`listingtest.Run`), proto3 and Presence storage, scope, token binding, time cap and deadline, corrupt records, `EnsureIndexes` and explain-plan index checks (`mongotest.AssertIndexedSorts`), an application BSON registry read by the evaluator as the server stores it |
| `internal/dbtest` | The server helpers: one container per server and test binary, a fresh database per test, see [its README](internal/dbtest/README.md)                                                                                                                                                                                                                                                                                       |

Each live filter run compares the rows the server selects with the rows the default evaluator (for MongoDB, the evaluator with the MongoDB profile)
selects over `filtertest.ConformanceRecords`. The expectation tables and decoders the live suites share with the backends' unit tests are exported by
small test-support packages of the backend modules that import no driver:
[`backend/postgres/postgrestest`](../../backend/postgres/postgrestest/README.md),
[`backend/mariadb/mariadbtest`](../../backend/mariadb/mariadbtest/README.md),
[`backend/clickhouse/clickhousetest`](../../backend/clickhouse/clickhousetest/README.md) and
[`backend/mongo/mongotest`](../../backend/mongo/mongotest/README.md) (which imports the MongoDB driver, as its module does).

## Adding a suite

A new live suite is a `_test.go` file with `//go:build integration_docker` in the package of its backend (a new backend gets a package with a
`doc.go` and a `main_test.go` whose `TestMain` calls `dbtest.Run`). A new server is added to `internal/dbtest` as its README describes. Fixtures
the unit tests of a backend module also need go into the backend's test-support package, never into this module. A suite for a backend module
this module does not require yet adds a `require` and a `replace` of it (and of the modules it requires) to `go.mod`.

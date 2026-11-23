// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package dbtest

import (
	"context"
	"database/sql"
	"net/url"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"

	_ "github.com/ClickHouse/clickhouse-go/v2" // registers the "clickhouse" database/sql driver
	_ "github.com/jackc/pgx/v5/stdlib"         // registers the "pgx" database/sql driver

	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Server credentials. The containers are private to the test binary and listen on a random local port.
const (
	user     = "root"
	password = "aip-integration"
	adminDB  = "aip"
)

// maxConnections is the server connection limit: parallel tests each hold a pool of up to maxOpenConns.
const maxConnections = "500"

// postgres is PostgreSQL 16 through pgx's database/sql driver. Its databases use UTF-8 and the server runs in UTC.
var postgres = postgresServer(PostgresImage)

var postgres17 = postgresServer(Postgres17Image)

func postgresServer(image string) *engine {
	return &engine{
		name:   "PostgreSQL",
		driver: "pgx",
		start: func(ctx context.Context) (testcontainers.Container, string, error) {
			ctr, err := tcpostgres.Run(ctx, image,
				tcpostgres.WithDatabase(adminDB), tcpostgres.WithUsername(user), tcpostgres.WithPassword(password),
				tcpostgres.BasicWaitStrategies(),
				testcontainers.WithCmdArgs("-c", "max_connections="+maxConnections, "-c", "timezone=UTC"),
			)
			if ctr == nil {
				return nil, "", err
			}

			if err != nil {
				return ctr, "", err
			}

			dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")

			return ctr, dsn, err
		},
		database: urlDatabase,
		create:   "CREATE DATABASE %s ENCODING 'UTF8' TEMPLATE template0",
		drop:     "DROP DATABASE IF EXISTS %s WITH (FORCE)",
	}
}

// mariadb is MariaDB 11 through go-sql-driver/mysql. Its databases default to utf8mb4 with the utf8mb4_nopad_bin collation the
// listing storage contract requires; connections parse DATETIME values as UTC time.Time.
var mariadb = &engine{
	name:   "MariaDB",
	driver: "mysql",
	start: func(ctx context.Context) (testcontainers.Container, string, error) {
		ctr, err := tcmariadb.Run(ctx, MariaDBImage,
			tcmariadb.WithDatabase(adminDB), tcmariadb.WithUsername(user), tcmariadb.WithPassword(password),
			testcontainers.WithCmd("mariadbd", "--max-connections="+maxConnections, "--default-time-zone=+00:00"),
		)
		if ctr == nil {
			return nil, "", err
		}

		if err != nil {
			return ctr, "", err
		}

		dsn, err := ctr.ConnectionString(ctx, "parseTime=true", "loc=UTC", "multiStatements=true")

		return ctr, dsn, err
	},
	database: func(admin, database string) (string, error) {
		cfg, err := mysql.ParseDSN(admin)
		if err != nil {
			return "", err
		}

		cfg.DBName = database

		return cfg.FormatDSN(), nil
	},
	create: "CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_nopad_bin",
	drop:   "DROP DATABASE IF EXISTS %s",
}

// clickhouse is ClickHouse through clickhouse-go's database/sql driver (native protocol). The server and its sessions run in
// UTC; databases use the default Atomic engine and are dropped synchronously.
var clickhouse = &engine{
	name:   "ClickHouse",
	driver: "clickhouse",
	start: func(ctx context.Context) (testcontainers.Container, string, error) {
		ctr, err := tcclickhouse.Run(ctx, ClickHouseImage,
			tcclickhouse.WithDatabase(adminDB), tcclickhouse.WithUsername(user), tcclickhouse.WithPassword(password),
			testcontainers.WithEnv(map[string]string{"TZ": "UTC"}),
		)
		if ctr == nil {
			return nil, "", err
		}

		if err != nil {
			return ctr, "", err
		}

		dsn, err := ctr.ConnectionString(ctx)

		return ctr, dsn, err
	},
	database: urlDatabase,
	create:   "CREATE DATABASE %s",
	drop:     "DROP DATABASE IF EXISTS %s SYNC",
}

// urlDatabase replaces the database (the path) of a URL DSN.
func urlDatabase(admin, database string) (string, error) {
	u, err := url.Parse(admin)
	if err != nil {
		return "", err
	}

	u.Path = "/" + database

	return u.String(), nil
}

// Postgres returns a connection pool to a new, empty PostgreSQL database that is dropped when tb ends. The server starts on
// the first call of the test binary. It skips tb when Docker is unreachable.
func Postgres(tb testing.TB) *sql.DB {
	tb.Helper()

	return postgres.open(tb)
}

// Postgres17 returns an isolated UTF8 database on the certified pg_c_utf8 server.
func Postgres17(tb testing.TB) *sql.DB {
	tb.Helper()
	return postgres17.open(tb)
}

// MariaDB returns a connection pool to a new, empty MariaDB database (utf8mb4, utf8mb4_nopad_bin) that is dropped when tb ends.
// Statements may hold several queries separated by semicolons. The server starts on the first call of the test binary. It
// skips tb when Docker is unreachable.
func MariaDB(tb testing.TB) *sql.DB {
	tb.Helper()

	return mariadb.open(tb)
}

// ClickHouse returns a connection pool to a new, empty ClickHouse database (Atomic engine) that is dropped when tb ends. The
// server starts on the first call of the test binary. It skips tb when Docker is unreachable.
func ClickHouse(tb testing.TB) *sql.DB {
	tb.Helper()

	return clickhouse.open(tb)
}

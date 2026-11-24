// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package dbtest_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

// TestIsolation checks every call gets an empty database of its own, with the server settings the backend tests rely on, and
// that the database is dropped when its test ends.
func TestIsolation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		open     func(testing.TB) *sql.DB
		current  string
		exists   string
		settings string
		marker   string // creates the table marker; "" is CREATE TABLE marker (id int)
		want     []string
	}{
		{
			name: "postgres", open: dbtest.Postgres, current: "SELECT current_database()",
			exists:   "SELECT count(*) FROM pg_database WHERE datname = $1",
			settings: "SELECT current_setting('server_encoding'), current_setting('TimeZone'), current_setting('server_version_num')::int >= 160000",
			want:     []string{"UTF8", "UTC", "true"},
		},
		{
			name: "postgres17", open: dbtest.Postgres17, current: "SELECT current_database()",
			exists:   "SELECT count(*) FROM pg_database WHERE datname = $1",
			settings: "SELECT current_setting('server_encoding'), current_setting('server_version_num')::int / 10000",
			want:     []string{"UTF8", "17"},
		},
		{
			name: "mariadb", open: dbtest.MariaDB, current: "SELECT DATABASE()",
			exists:   "SELECT count(*) FROM information_schema.schemata WHERE schema_name = ?",
			settings: "SELECT @@character_set_database, @@collation_database, @@time_zone, CAST(SUBSTRING_INDEX(VERSION(), '.', 1) AS UNSIGNED) >= 11",
			want:     []string{"utf8mb4", "utf8mb4_nopad_bin", "+00:00", "1"},
		},
		{
			name: "clickhouse", open: dbtest.ClickHouse, current: "SELECT currentDatabase()",
			exists:   "SELECT count() FROM system.databases WHERE name = ?",
			settings: "SELECT timezone(), toString(toUInt8(toUInt32(splitByChar('.', version())[1]) >= 26))",
			marker:   "CREATE TABLE marker (id Int32) ENGINE = MergeTree ORDER BY id",
			want:     []string{"UTC", "1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var names [2]string

			for i := range names {
				t.Run("open", func(t *testing.T) {
					db := tc.open(t)
					require.NoError(t, db.QueryRowContext(t.Context(), tc.current).Scan(&names[i]))

					marker := tc.marker
					if marker == "" {
						marker = "CREATE TABLE marker (id int)"
					}

					_, err := db.ExecContext(t.Context(), marker)
					require.NoError(t, err, "a fresh database has no table marker")

					got := make([]any, len(tc.want))
					for j := range got {
						got[j] = new(string)
					}

					require.NoError(t, db.QueryRowContext(t.Context(), tc.settings).Scan(got...))

					for j, w := range tc.want {
						require.Equal(t, w, *got[j].(*string))
					}
				})
			}

			require.NotEqual(t, names[0], names[1])

			admin := tc.open(t)

			for _, name := range names {
				var n int
				require.NoError(t, admin.QueryRowContext(t.Context(), tc.exists, name).Scan(&n))
				require.Zero(t, n, "database %s is dropped when its test ends", name)
			}
		})
	}
}

// TestMongoIsolation checks every Mongo call gets an empty database of its own on a MongoDB 8 server, with a client storing
// decimals as Decimal128 (the backend/mongo registry), and that the database is dropped when its test ends.
func TestMongoIsolation(t *testing.T) {
	t.Parallel()

	var names [2]string

	for i := range names {
		t.Run("open", func(t *testing.T) {
			db := dbtest.Mongo(t)
			names[i] = db.Name()

			collections, err := db.ListCollectionNames(t.Context(), bson.D{})
			require.NoError(t, err)
			require.Empty(t, collections, "a fresh database is empty")

			_, err = db.Collection("marker").InsertOne(t.Context(), bson.D{{Key: "price", Value: decimal.RequireFromString("1.5")}})
			require.NoError(t, err)

			var stored bson.Raw
			require.NoError(t, db.Collection("marker").FindOne(t.Context(), bson.D{}).Decode(&stored))
			require.Equal(t, bson.TypeDecimal128, stored.Lookup("price").Type)

			var info bson.Raw
			require.NoError(t, db.RunCommand(t.Context(), bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info))
			require.GreaterOrEqual(t, info.Lookup("versionArray", "0").AsInt64(), int64(8))
		})
	}

	require.NotEqual(t, names[0], names[1])

	databases, err := dbtest.Mongo(t).Client().ListDatabaseNames(t.Context(), bson.D{})
	require.NoError(t, err)

	for _, name := range names {
		require.NotContains(t, databases, name, "database %s is dropped when its test ends", name)
	}
}

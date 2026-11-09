// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
)

// multibyteBreakouts are the values that break out of a byte-wise escaped literal in a multibyte client encoding of
// PostgreSQL or MariaDB. ClickHouse has no client encoding, but its inline literals escape every byte >= 0x80 as \xNN too,
// and the server must read them back as the original UTF-8.
var multibyteBreakouts = []string{"丿\\' OR 1=1 -- ", "丁\\' OR 1=1 -- ", "丿\\", "丁'", "жё\\'", "😀\\'", "a\\b'c"}

// TestInlineNonASCIILive runs inline filters over non-ASCII values: each must select exactly the row that holds its value.
func TestInlineNonASCIILive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), "CREATE TABLE inline_enc (id String, name String) ENGINE = MergeTree ORDER BY id")
	require.NoError(t, err)

	rows := [][]any{{"plain", "alice"}}
	for i, v := range multibyteBreakouts {
		rows = append(rows, []any{strconv.Itoa(i), v})
	}

	insertRows(t, db, "inline_enc", []string{"id", "name"}, rows)

	tr, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)

	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	for _, c := range inlineBreakoutCases() {
		where, err := tr.TranslateInline(filtertest.MustParseFilter(t, c.expr))
		require.NoError(t, err)

		got, err := connIDs(t, conn, "SELECT id FROM inline_enc WHERE "+where+" ORDER BY id")
		require.NoError(t, err, "%s: %s", c.expr, where)
		require.Equal(t, c.want, got, "%s: %s", c.expr, where)
	}
}

// inlineBreakoutCase is a filter over a multibyteBreakouts value and the ids (sorted as text) it selects.
type inlineBreakoutCase struct {
	expr string
	want []string
}

// inlineBreakoutCases compares, tests membership and prefixes of every multibyteBreakouts value.
func inlineBreakoutCases() []inlineBreakoutCase {
	var cases []inlineBreakoutCase

	for i, v := range multibyteBreakouts {
		quoted := strconv.Quote(v)
		cases = append(cases, inlineBreakoutCase{"name == " + quoted, []string{strconv.Itoa(i)}},
			inlineBreakoutCase{`name in ["x", ` + quoted + "]", []string{strconv.Itoa(i)}})

		var prefixed []string

		for j, w := range multibyteBreakouts {
			if strings.HasPrefix(w, v) {
				prefixed = append(prefixed, strconv.Itoa(j))
			}
		}

		cases = append(cases, inlineBreakoutCase{"name.startsWith(" + quoted + ")", prefixed})
	}

	return cases
}

// connIDs runs query on conn and returns the ids it selects.
func connIDs(t *testing.T, conn *sql.Conn, query string) ([]string, error) {
	rows, err := conn.QueryContext(t.Context(), query)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}

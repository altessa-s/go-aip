// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mariadb_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
)

// multibyteBreakouts are values whose UTF-8 bytes, read in a multibyte connection character set, end in a lead byte that
// swallows the backslash after it: U+4E3F ends in BF (gbk, big5), U+4E01 in 81 (sjis, cp932). Rendered with a byte-wise
// escape, the escaping backslash is eaten, the doubled quote closes the literal and `OR 1=1` becomes SQL.
var multibyteBreakouts = []string{"丿\\' OR 1=1 -- ", "丁\\' OR 1=1 -- ", "丿\\", "丁'", "жё\\'", "😀\\'", "a\\b'c"}

// TestInlineConnectionCharsetsLive runs inline filters over a connection in every multibyte character set MariaDB offers as
// a connection character set: each must select exactly the row that holds its value, as the parameterized form does.
func TestInlineConnectionCharsetsLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	_, err := db.ExecContext(t.Context(), "CREATE TABLE inline_enc (id VARCHAR(8) PRIMARY KEY, name VARCHAR(64))")
	require.NoError(t, err)

	_, err = db.ExecContext(t.Context(), "INSERT INTO inline_enc VALUES ('plain', 'alice')")
	require.NoError(t, err)

	for i, v := range multibyteBreakouts {
		_, err := db.ExecContext(t.Context(), "INSERT INTO inline_enc VALUES (?, ?)", strconv.Itoa(i), v)
		require.NoError(t, err)
	}

	tr, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)

	for _, charset := range []string{"gbk", "big5", "sjis", "cp932", "euckr", "ujis", "utf8mb4"} {
		conn, err := db.Conn(t.Context())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		_, err = conn.ExecContext(t.Context(), "SET NAMES "+charset)
		require.NoError(t, err)

		for _, c := range inlineBreakoutCases() {
			where, err := tr.TranslateInline(filtertest.MustParseFilter(t, c.expr))
			require.NoError(t, err)

			got, err := connIDs(t, conn, "SELECT id FROM inline_enc WHERE "+where+" ORDER BY id")
			require.NoError(t, err, "%s %s: %s", charset, c.expr, where)
			require.Equal(t, c.want, got, "%s %s: %s", charset, c.expr, where)
		}
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

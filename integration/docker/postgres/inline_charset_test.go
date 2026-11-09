// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
)

// multibyteBreakouts are values whose UTF-8 bytes, read in a multibyte client encoding, end in a lead byte that swallows the
// backslash after it: U+4E3F ends in BF (GBK, BIG5, GB18030), U+4E01 in 81 (SJIS). Rendered with a byte-wise escape, the
// escaping backslash of the quote is eaten and `OR 1=1` becomes SQL.
var multibyteBreakouts = []string{"丿\\' OR 1=1 -- ", "丁\\' OR 1=1 -- ", "丿\\", "丁'", "жё\\'", "😀\\'", "a\\b'c"}

// TestInlineClientEncodingsLive runs inline filters over a connection in every multibyte client encoding PostgreSQL
// supports: each must select exactly the row that holds its value, as the parameterized form does.
func TestInlineClientEncodingsLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE inline_enc (id text PRIMARY KEY, name text COLLATE "C")`)
	require.NoError(t, err)

	_, err = db.ExecContext(t.Context(), `INSERT INTO inline_enc VALUES ('plain', 'alice')`)
	require.NoError(t, err)

	for i, v := range multibyteBreakouts {
		_, err := db.ExecContext(t.Context(), `INSERT INTO inline_enc VALUES ($1, $2)`, strconv.Itoa(i), v)
		require.NoError(t, err)
	}

	scalar, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	schema, err := postgres.NewSchema(fields)
	require.NoError(t, err)
	schemaTr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), schema)
	require.NoError(t, err)

	translators := map[string]func(filter.Node) (string, error){"scalar": scalar.TranslateInline, "schema": schemaTr.TranslateInline}

	for _, encoding := range []string{"GBK", "SJIS", "BIG5", "GB18030", "UHC", "UTF8"} {
		conn, err := db.Conn(t.Context())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		_, err = conn.ExecContext(t.Context(), "SET client_encoding = '"+encoding+"'")
		require.NoError(t, err)

		for name, translate := range translators {
			for _, c := range inlineBreakoutCases() {
				where, err := translate(filtertest.MustParseFilter(t, c.expr))
				require.NoError(t, err)

				got, err := connIDs(t, conn, "SELECT id FROM inline_enc WHERE "+where+" ORDER BY id")
				require.NoError(t, err, "%s %s %s: %s", encoding, name, c.expr, where)
				require.Equal(t, c.want, got, "%s %s %s: %s", encoding, name, c.expr, where)
			}
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

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

// The built-in dialects' styles.
var (
	pgStyle    = sqlbase.Style{Quote: '"', Numbered: true}
	mysqlStyle = sqlbase.Style{Quote: '`'}
)

func TestStyleTable(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "t; DROP TABLE x", "a.b.c", "1tasks", `"quoted"`, "tab le", ".t", "s.", "a..b", "s.1t", "t\n", "t-x", "ü",
		strings.Repeat("t", sqlbase.PortableMaxIdentLen+1), "s." + strings.Repeat("t", sqlbase.PortableMaxIdentLen+1)} {
		_, err := pgStyle.Table(name)
		require.ErrorIs(t, err, sqlbase.ErrInvalidTableName, name)
	}

	for _, tc := range []struct {
		style      sqlbase.Style
		name, want string
	}{
		{pgStyle, "events", `"events"`},
		{pgStyle, "app.events", `"app"."events"`},
		{mysqlStyle, "app.events", "`app`.`events`"},
		{mysqlStyle, "_T1", "`_T1`"},
		{pgStyle, "a1_.B_2", `"a1_"."B_2"`},
	} {
		got, err := tc.style.Table(tc.name)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got)
	}

	long := strings.Repeat("t", sqlbase.PortableMaxIdentLen)
	_, err := mysqlStyle.Table(long + "." + long)
	require.NoError(t, err, "the limit applies per part and is inclusive")
}

func TestStyleIndexName(t *testing.T) {
	t.Parallel()
	require.Equal(t, `"tasks_due_idx"`, pgStyle.IndexName("tasks", "due"))
	require.Equal(t, "`tasks_due_idx`", mysqlStyle.IndexName("tasks", "due"))

	long := strings.Repeat("t", 62)
	a, b := pgStyle.IndexName(long+"a", "due"), pgStyle.IndexName(long+"b", "due")
	require.NotEqual(t, a, b, "truncated names must stay distinct")

	for _, n := range []string{a, b} {
		unquoted := strings.Trim(n, `"`)
		require.LessOrEqual(t, len(unquoted), sqlbase.PortableMaxIdentLen)
		require.True(t, strings.HasPrefix(unquoted, "tttt"), "a readable prefix is kept")
		require.True(t, strings.HasSuffix(unquoted, "_due_idx"))
	}

	require.Equal(t, a, pgStyle.IndexName(long+"a", "due"), "the name is deterministic")
}

func TestStyleBind(t *testing.T) {
	t.Parallel()

	q := "UPDATE t SET a = ? WHERE id = ? AND b = ?"
	require.Equal(t, "UPDATE t SET a = $1 WHERE id = $2 AND b = $3", pgStyle.Bind(q, 1))
	require.Equal(t, "UPDATE t SET a = $9 WHERE id = $10 AND b = $11", pgStyle.Bind(q, 9))
	require.Equal(t, q, mysqlStyle.Bind(q, 5))
	require.Equal(t, "SELECT 1", pgStyle.Bind("SELECT 1", 1))
}

func TestQualifiedParts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, table, schema string }{
		{"events", "events", ""},
		{"app.events", "events", "app"},
	} {
		require.Equal(t, tc.table, sqlbase.Unqualified(tc.name))
		require.Equal(t, tc.schema, sqlbase.Qualifier(tc.name))
	}
}

func TestStyleIndexNameLongSuffix(t *testing.T) {
	t.Parallel()

	suffix := strings.Repeat("s", sqlbase.PortableMaxIdentLen)
	a := pgStyle.IndexName("tasks", suffix+"a")
	b := pgStyle.IndexName("tasks", suffix+"b")

	require.Len(t, a, sqlbase.PortableMaxIdentLen+2)
	require.NotEqual(t, a, b)
	require.Equal(t, a, pgStyle.IndexName("tasks", suffix+"a"))
}

func TestStyleQuoteIdent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		style      sqlbase.Style
		name, want string
	}{
		{pgStyle, "id", `"id"`},
		{pgStyle, `a"b`, `"a""b"`},
		{pgStyle, "a`b", "\"a`b\""},
		{mysqlStyle, "id", "`id`"},
		{mysqlStyle, "a`b", "`a``b`"},
		{mysqlStyle, `a"b`, "`a\"b`"},
	} {
		require.Equal(t, tc.want, tc.style.QuoteIdent(tc.name), tc.name)
	}
}

func TestIsPortableIdent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want bool
	}{
		{"id", true},
		{"_a1", true},
		{strings.Repeat("x", sqlbase.PortableMaxIdentLen), true},
		{"", false},
		{"1a", false},
		{"a-b", false},
		{"a.b", false},
		{"ü", false},
		{strings.Repeat("x", sqlbase.PortableMaxIdentLen+1), false},
	} {
		require.Equal(t, tc.want, sqlbase.IsPortableIdent(tc.name), tc.name)
	}
}

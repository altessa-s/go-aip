// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// anyColumn admits every column name, as a custom column validator may.
func anyColumn(string) error { return nil }

// TestSchemaRejectsTruncatedIdentifiers pins PostgreSQL's 63-byte identifier limit on the schema translator: a longer
// column part, collation or alias prefix would be silently truncated by the server.
func TestSchemaRejectsTruncatedIdentifiers(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 63)

	for name, column := range map[string]string{"column": long + "_shadow", "table": long + "x.c", "qualified column": "t." + long + "x"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar(column, sqlbase.Text)},
				sqlbase.WithSchemaColumnValidator(anyColumn))
			require.NoError(t, err)
			schema, err := postgres.NewSchema(fields)
			require.NoError(t, err)
			tr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
			require.NoError(t, err)

			got, args, err := tr.Translate(filtertest.MustParseFilter(t, `name == "x"`))
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
			require.Empty(t, args)
		})
	}

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar(long+"."+long, sqlbase.Text)})
	require.NoError(t, err)
	schema, err := postgres.NewSchema(fields)
	require.NoError(t, err)
	tr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)
	got, _, err := tr.Translate(filtertest.MustParseFilter(t, `name == "x"`))
	require.NoError(t, err, "parts at the limit")
	require.Contains(t, got, `"`+long+`"."`+long+`"`)

	for name, opt := range map[string]postgres.SchemaOption{
		"collation":    postgres.WithSchemaTextCollation(strings.Repeat("c", 64)),
		"alias prefix": postgres.WithSchemaArrayAliasPrefix(strings.Repeat("p", 56)),
	} {
		_, err := postgres.NewSchema(fields, opt, postgres.WithSchemaTextCompare(strings.Compare))
		require.ErrorIs(t, err, filter.ErrInvalidOptions, name)
	}

	_, err = postgres.NewSchema(fields, postgres.WithSchemaTextCollation(strings.Repeat("c", 63)),
		postgres.WithSchemaArrayAliasPrefix(strings.Repeat("p", 55)), postgres.WithSchemaTextCompare(strings.Compare))
	require.NoError(t, err)
}

// TestSchemaInlineIsASCII pins the pure-ASCII inline output of the schema translator: text literals and JSON path keys
// escape their non-ASCII code points, and a non-ASCII column or collation name cannot be rendered inline at all.
func TestSchemaInlineIsASCII(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text),
		"city": sqlbase.JSONPath("doc", sqlbase.Text, "адрес", "город"),
	})
	require.NoError(t, err)

	for _, contract := range []bool{false, true} {
		schema, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(contract))
		require.NoError(t, err)
		tr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
		require.NoError(t, err)

		if contract {
			continue // the listing contract has no JSON paths
		}

		got, err := tr.TranslateInline(filtertest.MustParseFilter(t, `name == "丿\\' OR 1=1 -- " && city == "Осло"`))
		require.NoError(t, err)
		require.Contains(t, got, `(E'\u4e3f\\'' OR 1=1 -- ')::text`)
		require.Contains(t, got, `ARRAY[E'\u0430\u0434\u0440\u0435\u0441', E'\u0433\u043e\u0440\u043e\u0434']::text[]`)
		require.Contains(t, got, `(E'\u041e\u0441\u043b\u043e')::text`)
		requireASCII(t, got)
	}

	custom, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("имя", sqlbase.Text)},
		sqlbase.WithSchemaColumnValidator(anyColumn))
	require.NoError(t, err)

	for name, opts := range map[string][]postgres.SchemaOption{
		"column":    nil,
		"collation": {postgres.WithSchemaTextCollation("ru-x-icu-é"), postgres.WithSchemaTextCompare(strings.Compare)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := custom
			if name == "collation" {
				source = fields
			}

			schema, err := postgres.NewSchema(source, opts...)
			require.NoError(t, err)
			tr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
			require.NoError(t, err)

			node := filtertest.MustParseFilter(t, `name == "x"`)
			_, _, err = tr.Translate(node)
			require.NoError(t, err, "the bound form carries the name as is")

			got, err := tr.TranslateInline(node)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
		})
	}
}

func requireASCII(t *testing.T, sql string) {
	t.Helper()

	for i := range len(sql) {
		require.Less(t, sql[i], byte(0x80), "non-ASCII byte at %d: %s", i, sql)
	}
}

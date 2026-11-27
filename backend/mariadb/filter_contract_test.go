// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
)

func contractTranslator(t testing.TB) *mariadb.SchemaFilterTranslator {
	t.Helper()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name":    sqlbase.Scalar("t.name", sqlbase.Text),
		"note":    sqlbase.PresenceScalar("note", sqlbase.Text),
		"tags":    sqlbase.Array("t.tags", sqlbase.Text),
		"numbers": sqlbase.Array("numbers", sqlbase.Integer),
		"floats":  sqlbase.Array("floats", sqlbase.Float),
		"flags":   sqlbase.Array("flags", sqlbase.Boolean),
		"prices":  sqlbase.Array("prices", sqlbase.Numeric),
		"times":   sqlbase.Array("times", sqlbase.Timestamp),
		"price":   sqlbase.Scalar("price", sqlbase.Numeric),
		"active":  sqlbase.PresenceScalar("active", sqlbase.Boolean),
	})
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})), contractSchema)
	require.NoError(t, err)

	return tr
}

func TestContractSQLAndArguments(t *testing.T) {
	t.Parallel()

	tr := contractTranslator(t)
	for _, tc := range []struct {
		expr, sql string
		args      []any
	}{
		{`name == "a "`, "(`t`.`name` = ?)", []any{"a "}},
		{`name != "a"`, "(`t`.`name` <> ?)", []any{"a"}},
		{`name < "a"`, "(`t`.`name` < ?)", []any{"a"}},
		{`name <= "a"`, "(`t`.`name` <= ?)", []any{"a"}},
		{`name > "a"`, "(`t`.`name` > ?)", []any{"a"}},
		{`name >= "a"`, "(`t`.`name` >= ?)", []any{"a"}},
		{`name in ["a"] && name in ["b"]`, "((`t`.`name` IN (?)) AND (`t`.`name` IN (?)))", []any{"a", "b"}},
		{`name in ["a"] || !(name in ["b"])`, "((`t`.`name` IN (?)) OR (`t`.`name` NOT IN (?)))", []any{"a", "b"}},
		{`!(tags in ["a"] && tags in ["b"])`, "((NOT JSON_OVERLAPS(`t`.`tags`, ?)) OR (NOT JSON_OVERLAPS(`t`.`tags`, ?)))", []any{`["a"]`, `["b"]`}},
		{`tags in ["a"] || tags in ["b"]`, "(JSON_OVERLAPS(`t`.`tags`, ?) OR JSON_OVERLAPS(`t`.`tags`, ?))", []any{`["a"]`, `["b"]`}},
		{`tags == "a"`, "JSON_CONTAINS(`t`.`tags`, ?)", []any{`"a"`}},
		{`tags != "a"`, "(NOT JSON_CONTAINS(`t`.`tags`, ?))", []any{`"a"`}},
		{`"a" in tags`, "JSON_CONTAINS(`t`.`tags`, ?)", []any{`"a"`}},
		{`numbers == 2`, "JSON_CONTAINS(`numbers`, ?)", []any{"2"}},
		{`flags == true`, "JSON_CONTAINS(`flags`, ?)", []any{"true"}},
		{`flags in [true, false]`, "JSON_OVERLAPS(`flags`, ?)", []any{"[true,false]"}},
		{`prices == 2`, "JSON_CONTAINS(`prices`, ?)", []any{"2"}},
		{`prices in [2, 10]`, "JSON_OVERLAPS(`prices`, ?)", []any{"[2,10]"}},
		{`price == 10000000000000000.01`, "(`price` = CAST(? AS DECIMAL(65,30)))", []any{"10000000000000000.01"}},
		{`note == null`, "((`note`) IS NULL)", nil},
		{`note != null`, "((`note`) IS NOT NULL)", nil},
		{`name == null`, "(`t`.`name` = ?)", []any{""}},
		{`tags == null`, "(JSON_LENGTH(`t`.`tags`) = 0)", nil},
		{`size(note) == 0`, "(COALESCE(CHAR_LENGTH(`note`), 0) = ?)", []any{int64(0)}},
		{`!(note > "n")`, "(NOT (`note` > ?))", []any{"n"}},
		// A null presence column differs from every value, is in no list and contains no string, as in the default evaluator.
		{`note != "a"`, "(`note` <> ? OR `note` IS NULL)", []any{"a"}},
		{`!(note == "a")`, "(`note` <> ? OR `note` IS NULL)", []any{"a"}},
		{`!(note != "a")`, "(`note` = ?)", []any{"a"}},
		{`!(note in ["a", "b"])`, "(`note` NOT IN (?, ?) OR `note` IS NULL)", []any{"a", "b"}},
		{`note in ["a", null]`, "(`note` IN (?) OR `note` IS NULL)", []any{"a"}},
		{`!(note in ["a", null])`, "(`note` NOT IN (?))", []any{"a"}},
		{`!note.contains("a")`, "(NOT (LOCATE(?, `note`) > 0) OR `note` IS NULL)", []any{"a"}},
		{`!(note.contains("a") || name == "x")`, "((NOT (LOCATE(?, `note`) > 0) OR `note` IS NULL) AND (`t`.`name` <> ?))", []any{"a", "x"}},
		{`!(note == "a" && note > "b")`, "((`note` <> ? OR `note` IS NULL) OR (NOT (`note` > ?)))", []any{"a", "b"}},
		{`!(note == null)`, "((`note`) IS NOT NULL)", nil},
		{`!(tags != "a")`, "JSON_CONTAINS(`t`.`tags`, ?)", []any{`"a"`}},
		{`note in [null]`, "((`note`) IS NULL)", nil},
		{`!(note in [null])`, "((`note`) IS NOT NULL)", nil},
		{`name in [null]`, "FALSE", nil},
		{`!(name in [null])`, "TRUE", nil},
		{`!(name in [])`, "TRUE", nil},
		{`!(tags in [])`, "TRUE", nil},
		{`note == note`, "(`note` <=> `note`)", nil},
		{`!(note == note)`, "(NOT (`note` <=> `note`))", nil},
		{`size(name) == null`, "FALSE", nil},
		{`!(size(name) == null)`, "((CHAR_LENGTH(`t`.`name`)) IS NOT NULL)", nil},
		{`!active`, "(NOT (`active`))", nil},
		{
			`active != (name == "x")`,
			"(`active` <> (CASE WHEN (`t`.`name` = ?) THEN TRUE WHEN (`t`.`name` <> ?) THEN FALSE END) OR (`active` IS NULL AND " +
				"(CASE WHEN (`t`.`name` = ?) THEN TRUE WHEN (`t`.`name` <> ?) THEN FALSE END) IS NOT NULL))",
			[]any{"x", "x", "x", "x"},
		},
		{`active != (size(name) > 1)`, "(`active` <> (CASE WHEN (CHAR_LENGTH(`t`.`name`) > ?) THEN TRUE WHEN (NOT (CHAR_LENGTH(`t`.`name`) > ?)) THEN FALSE END) OR " +
			"(`active` IS NULL AND (CASE WHEN (CHAR_LENGTH(`t`.`name`) > ?) THEN TRUE WHEN (NOT (CHAR_LENGTH(`t`.`name`) > ?)) THEN FALSE END) IS NOT NULL))",
			[]any{int64(1), int64(1), int64(1), int64(1)}},
		{`name.endsWith("a ")`, "((CHAR_LENGTH(`t`.`name`) >= CHAR_LENGTH(?) AND RIGHT(`t`.`name`, CHAR_LENGTH(?)) = ?))", []any{"a ", "a ", "a "}},
		{`name.startsWith("a")`, "(LOCATE(?, `t`.`name`) = 1)", []any{"a"}},
		{`name.contains("a")`, "(LOCATE(?, `t`.`name`) > 0)", []any{"a"}},
		{`name.matches("a$")`, "(`t`.`name` REGEXP ?)", []any{`a\z`}},
		{`name.matches("^b")`, "(`t`.`name` REGEXP ?)", []any{`^b`}},
		{`name.matches("(?i)alpha")`, "(`t`.`name` REGEXP ?)", []any{`(?i)alpha`}},
		{`name.matches("alpha")`, "(`t`.`name` REGEXP ?)", []any{`alpha`}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.sql, sql)
			require.Equal(t, tc.args, args)
		})
	}
}

func TestContractMatchNoneArray(t *testing.T) {
	t.Parallel()

	sql, args, err := contractTranslator(t).Translate(listing.MatchNone("tags"))
	require.NoError(t, err)
	require.Equal(t, "FALSE", sql)
	require.Empty(t, args)
}

func TestContractRejectsNonLiteralArrayOperands(t *testing.T) {
	t.Parallel()

	tr := contractTranslator(t)
	for _, expr := range []string{`tags > name`, `name in [note]`} {
		_, _, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}
}

func TestContractTypedJSONTable(t *testing.T) {
	t.Parallel()

	tr := contractTranslator(t)
	for _, tc := range []struct {
		expr, typ, pred string
		args            []any
	}{
		{`numbers > 9007199254740992`, "BIGINT", "(`_aip_element_0`.`element` > ?)", []any{int64(9007199254740992)}},
		{`floats < 2.5`, "DOUBLE", "(`_aip_element_0`.`element` < ?)", []any{2.5}},
		{`prices < 2`, "DECIMAL(65,30)", "(`_aip_element_0`.`element` < CAST(? AS DECIMAL(65,30)))", []any{"2"}},
		{`flags > false`, "BOOLEAN", "(`_aip_element_0`.`element` > ?)", []any{false}},
		{`tags.startsWith("a")`, "LONGTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_nopad_bin", "LOCATE(?, `_aip_element_0`.`element`) = 1", []any{"a"}},
		{`tags.matches("a$")`, "LONGTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_nopad_bin", "`_aip_element_0`.`element` REGEXP ?", []any{`a\z`}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Contains(t, sql, "COLUMNS (`element` "+tc.typ+" PATH '$')")
			require.Contains(t, sql, "WHERE "+tc.pred)
			require.Equal(t, tc.args, args)
			require.Equal(t, len(args), strings.Count(sql, "?"))
		})
	}

	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC)
	for _, op := range []filter.Operator{filter.OpEqual, filter.OpLT} {
		sql, args, err := tr.Translate(&filter.BinaryOpNode{Op: op, Left: &filter.IdentNode{Name: "times"}, Right: &filter.LiteralNode{Value: timestamp}})
		require.NoError(t, err)

		if op == filter.OpEqual {
			require.Equal(t, "JSON_CONTAINS(`times`, ?)", sql)
			require.Equal(t, []any{`"2026-01-02T03:04:05.123Z"`}, args)
		} else {
			require.Contains(t, sql, "`element` DATETIME(3) PATH '$'")
			require.Contains(t, sql, "< CAST(? AS DATETIME(3))")
			require.Equal(t, []any{timestamp}, args)
		}
	}
}

func TestContractOptionRequired(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	plain, err := mariadb.NewSchema(fields)
	require.NoError(t, err)
	_, err = mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), plain)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	_, err = mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), nil)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	_, err = mariadb.NewSchema(nil, mariadb.WithSchemaContractMode(true))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	require.ErrorIs(t, (*mariadb.Schema)(nil).ValidatePolicy(filtertest.MustPolicy(t, filter.WithAnyField(true))), filter.ErrInvalidOptions)
	require.False(t, (*mariadb.Schema)(nil).ContractMode())

	contractSchema, err := mariadb.NewSchema(fields, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	_, err = mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
	require.NoError(t, err)
}

func TestContractPresenceAndAliasSafety(t *testing.T) {
	t.Parallel()

	tr := contractTranslator(t)
	for _, tc := range []struct {
		field, sql string
		args       []any
	}{
		{"note", "((`note`) IS NOT NULL)", nil},
		{"name", "(`t`.`name` <> ?)", []any{""}},
		{"tags", "(JSON_LENGTH(`t`.`tags`) <> 0)", nil},
		{"price", "(`price` <> CAST(? AS DECIMAL(65,30)))", []any{"0"}},
	} {
		sql, args, err := tr.Translate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: tc.field}})
		require.NoError(t, err)
		require.Equal(t, tc.sql, sql)
		require.Equal(t, tc.args, args)
	}

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"tags": sqlbase.Array("_aip_element_0.tags", sqlbase.Text)})
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err = mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
	require.NoError(t, err)
	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `tags.endsWith("z")`))
	require.NoError(t, err)
	require.Contains(t, sql, "JSON_TABLE(`_aip_element_0`.`tags`")
	require.Contains(t, sql, "AS `_aip_element_1`")
	require.Equal(t, []any{"z", "z", "z"}, args)
}

func TestConfiguredDecimalDomain(t *testing.T) {
	t.Parallel()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"price": sqlbase.Scalar("price", sqlbase.Numeric)})
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true), mariadb.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 38, Scale: 35}))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
	require.NoError(t, err)
	n := filtertest.MustParseFilter(t, `price == "0.12345678901234567890123456789012345"`)
	sql, args, err := tr.Translate(n)
	require.NoError(t, err)
	require.Contains(t, sql, "DECIMAL(38,35)")
	require.Equal(t, []any{"0.12345678901234567890123456789012345"}, args)
	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price == "1000"`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}

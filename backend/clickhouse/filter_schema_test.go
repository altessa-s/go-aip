// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func schemaFields() map[string]sqlbase.Field {
	return map[string]sqlbase.Field{
		"name":    sqlbase.Scalar("name", sqlbase.Text),
		"note":    sqlbase.PresenceScalar("note", sqlbase.Text),
		"tags":    sqlbase.Array("tags", sqlbase.Text),
		"numbers": sqlbase.Array("numbers", sqlbase.Integer),
		"prices":  sqlbase.Array("prices", sqlbase.Numeric),
		"times":   sqlbase.Array("times", sqlbase.Timestamp),
		"price":   sqlbase.Scalar("price", sqlbase.Numeric),
		"active":  sqlbase.PresenceScalar("active", sqlbase.Boolean),
		"done":    sqlbase.Scalar("done", sqlbase.Boolean),
		"created": sqlbase.Scalar("created", sqlbase.Timestamp),
		"age":     sqlbase.Scalar("age", sqlbase.Integer),
		"level":   sqlbase.PresenceScalar("level", sqlbase.Integer),
		"score":   sqlbase.Scalar("score", sqlbase.Float),
		"data":    sqlbase.Scalar("data", sqlbase.Bytes),
		"nested":  sqlbase.Array("address.city", sqlbase.Text),
		"city":    sqlbase.JSONPath("profile", sqlbase.Text, "address", "city"),
		"first":   sqlbase.JSONPath("profile", sqlbase.Integer, "scores", "0"),
		"last":    sqlbase.JSONPath("profile", sqlbase.Float, "scores", "-1"),
		"vip":     sqlbase.JSONPath("profile", sqlbase.Boolean, "vip"),
		"seen":    sqlbase.JSONPath("profile", sqlbase.Timestamp, "seen"),
		"doc":     sqlbase.Scalar("doc", sqlbase.JSON),
		"uid":     sqlbase.Scalar("uid", sqlbase.UUID),
		"amount":  sqlbase.JSONPath("profile", sqlbase.Numeric, "amount"),
	}
}

// contractSchema binds fields to a ClickHouse schema in contract mode with opts.
func contractSchema(t testing.TB, fields *sqlbase.Schema, opts ...clickhouse.SchemaOption) *clickhouse.Schema {
	t.Helper()

	s, err := clickhouse.NewSchema(fields, append([]clickhouse.SchemaOption{clickhouse.WithSchemaContractMode(true)}, opts...)...)
	require.NoError(t, err)

	return s
}

func schemaTranslator(t testing.TB, opts ...clickhouse.SchemaOption) *clickhouse.SchemaFilterTranslator {
	t.Helper()

	schema, err := sqlbase.NewSchema(schemaFields())
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true}))
	tr, err := clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema(t, schema, opts...))
	require.NoError(t, err)

	return tr
}

func TestSchemaSQLAndArguments(t *testing.T) {
	t.Parallel()

	const (
		city = "if(JSONType(`profile`, 'address', 'city') IN ('String'), JSONExtract(`profile`, 'address', 'city', 'Nullable(String)'), NULL)"
		yes  = "(CASE WHEN (`name` = ?) THEN true WHEN (`name` != ?) THEN false END)"
	)

	tr := schemaTranslator(t)
	for _, tc := range []struct {
		expr, sql string
		args      []any
	}{
		{`name == "a "`, "(`name` = ?)", []any{"a "}},
		{`name != "a"`, "(`name` != ?)", []any{"a"}},
		{`name < "a"`, "(`name` < ?)", []any{"a"}},
		{`name >= "a"`, "(`name` >= ?)", []any{"a"}},
		{`"a" < name`, "(? < `name`)", []any{"a"}},
		{`name in ["a"] && name in ["b"]`, "((`name` IN (?)) AND (`name` IN (?)))", []any{"a", "b"}},
		{`name in ["a"] || !(name in ["b"])`, "((`name` IN (?)) OR (`name` NOT IN (?)))", []any{"a", "b"}},
		{`!(name == "a" || age > 1)`, "((`name` != ?) AND (NOT (`age` > ?)))", []any{"a", int64(1)}},
		{`age == 1 && score > 1`, "((`age` = ?) AND (`score` > ?))", []any{int64(1), int64(1)}},
		// Arrays: native Array(T) without NULL elements, so no test is ever unknown.
		{`tags == "a"`, "has(`tags`, ?)", []any{"a"}},
		{`tags != "a"`, "(NOT has(`tags`, ?))", []any{"a"}},
		{`!(tags != "a")`, "has(`tags`, ?)", []any{"a"}},
		{`"a" in tags`, "has(`tags`, ?)", []any{"a"}},
		{`!("a" in tags)`, "(NOT has(`tags`, ?))", []any{"a"}},
		{`null in tags`, "false", nil},
		{`!(null in tags)`, "true", nil},
		{`tags in ["a", "b"]`, "hasAny(`tags`, [?, ?])", []any{"a", "b"}},
		{`!(tags in ["a", null])`, "(NOT hasAny(`tags`, [?]))", []any{"a"}},
		{`tags in [null]`, "false", nil},
		{`!(tags in [])`, "true", nil},
		{`tags > "b"`, "arrayExists(_aip_e -> _aip_e > ?, `tags`)", []any{"b"}},
		{`!(tags > "b")`, "(NOT arrayExists(_aip_e -> _aip_e > ?, `tags`))", []any{"b"}},
		{`numbers <= 2`, "arrayExists(_aip_e -> _aip_e <= ?, `numbers`)", []any{int64(2)}},
		{`prices == 2`, "has(`prices`, CAST(? AS Decimal(65, 30)))", []any{"2"}},
		{`prices in [2, 10.5]`, "hasAny(`prices`, [CAST(? AS Decimal(65, 30)), CAST(? AS Decimal(65, 30))])", []any{"2", "10.5"}},
		{`tags.contains("x")`, "arrayExists(_aip_e -> position(_aip_e, ?) > 0, `tags`)", []any{"x"}},
		{`!tags.endsWith("x")`, "(NOT arrayExists(_aip_e -> endsWith(_aip_e, ?), `tags`))", []any{"x"}},
		{`size(tags) == 2`, "(length(`tags`) = ?)", []any{int64(2)}},
		{`tags == null`, "empty(`tags`)", nil},
		{`tags != null`, "notEmpty(`tags`)", nil},
		{`nested == "Oslo"`, "has(`address.city`, ?)", []any{"Oslo"}},
		// Optional NOT NULL scalars store their zero value.
		{`name == null`, "(`name` = ?)", []any{""}},
		{`!(name == null)`, "(`name` != ?)", []any{""}},
		{`age != null`, "(`age` != ?)", []any{int64(0)}},
		{`done == null`, "(`done` = ?)", []any{false}},
		{`score == null`, "(`score` = ?)", []any{float64(0)}},
		{`data == null`, "(`data` = ?)", []any{[]byte{}}},
		{`price == null`, "(`price` = CAST(? AS Decimal(65, 30)))", []any{"0"}},
		{`created == null`, "(`created` = toDateTime64(?, 3, 'UTC'))", []any{"1970-01-01 00:00:00.000"}},
		{`null == name`, "(`name` = ?)", []any{""}},
		{`size(name) == null`, "false", nil},
		{`!(size(name) == null)`, "((lengthUTF8(`name`)) IS NOT NULL)", nil},
		{`null == null`, "true", nil},
		// Nullable presence columns follow the default evaluator.
		{`note == null`, "((`note`) IS NULL)", nil},
		{`!(note == null)`, "((`note`) IS NOT NULL)", nil},
		{`note != "a"`, "(`note` != ? OR `note` IS NULL)", []any{"a"}},
		{`!(note == "a")`, "(`note` != ? OR `note` IS NULL)", []any{"a"}},
		{`!(note != "a")`, "(`note` = ?)", []any{"a"}},
		{`!(note > "n")`, "(NOT (`note` > ?))", []any{"n"}},
		{`!(note in ["a", "b"])`, "(`note` NOT IN (?, ?) OR `note` IS NULL)", []any{"a", "b"}},
		{`note in ["a", null]`, "(`note` IN (?) OR `note` IS NULL)", []any{"a"}},
		{`!(note in ["a", null])`, "(`note` NOT IN (?) AND `note` IS NOT NULL)", []any{"a"}},
		{`note in [null]`, "((`note`) IS NULL)", nil},
		{`!(note in [null])`, "((`note`) IS NOT NULL)", nil},
		{`name in [null]`, "false", nil},
		{`!(name in [null])`, "true", nil},
		{`!note.contains("a")`, "(NOT (position(`note`, ?) > 0) OR `note` IS NULL)", []any{"a"}},
		{`!name.contains("a")`, "(NOT (position(`name`, ?) > 0))", []any{"a"}},
		{`!(note.contains("a") || name == "x")`, "((NOT (position(`note`, ?) > 0) OR `note` IS NULL) AND (`name` != ?))", []any{"a", "x"}},
		{`note == note`, "COALESCE(`note` = `note`, `note` IS NULL AND `note` IS NULL)", nil},
		{`!(note == note)`, "COALESCE(`note` != `note`, (`note` IS NULL) != (`note` IS NULL))", nil},
		{`note == name`, "(`note` = `name`)", nil},
		{`note != name`, "(`note` != `name` OR `note` IS NULL)", nil},
		{`size(note) == 0`, "(COALESCE(lengthUTF8(`note`), 0) = ?)", []any{int64(0)}},
		{`level != size(name)`, "(`level` != lengthUTF8(`name`) OR `level` IS NULL)", nil},
		{`level == size(name)`, "(`level` = lengthUTF8(`name`))", nil},
		{`active`, "`active`", nil},
		{`!active`, "(NOT (`active`))", nil},
		{`done == true`, "(`done` = ?)", []any{true}},
		{`active != (name == "x")`, "(`active` != " + yes + " OR (`active` IS NULL AND " + yes + " IS NOT NULL))", []any{"x", "x", "x", "x"}},
		{`(name == "x") == active`, "(" + yes + " = `active`)", []any{"x", "x"}},
		{`(name == "x") != null`, "((" + yes + ") IS NOT NULL)", []any{"x", "x"}},
		{`(name == "x") == null`, "false", nil},
		// Typed values.
		{`price > 1.5`, "(`price` > CAST(? AS Decimal(65, 30)))", []any{"1.5"}},
		{`price == 10000000000000000.01`, "(`price` = CAST(? AS Decimal(65, 30)))", []any{"10000000000000000.01"}},
		{`created > timestamp("2026-01-02T03:04:05.123456Z")`, "(`created` > toDateTime64(?, 3, 'UTC'))", []any{"2026-01-02 03:04:05.123"}},
		{`times < timestamp("2026-01-01T00:00:00Z")`, "arrayExists(_aip_e -> _aip_e < toDateTime64(?, 3, 'UTC'), `times`)", []any{"2026-01-01 00:00:00.000"}},
		{`data == b"ab"`, "(`data` = ?)", []any{[]byte("ab")}},
		{`size(data) == 2`, "(length(`data`) = ?)", []any{int64(2)}},
		{`size("abc") == 3`, "(lengthUTF8(?) = ?)", []any{"abc", int64(3)}},
		// String predicates.
		{`name.startsWith("a")`, "(startsWith(`name`, ?))", []any{"a"}},
		{`name.endsWith("a ")`, "(endsWith(`name`, ?))", []any{"a "}},
		{`name.contains("%_")`, "(position(`name`, ?) > 0)", []any{"%_"}},
		{`name.matches("a$")`, "(match(`name`, ?))", []any{"a$"}},
		{`name.matches("(?i)alpha")`, "(match(`name`, ?))", []any{"(?i)alpha"}},
		// JSON paths: a missing key, a JSON null and another type read as NULL. JSONType guards each JSONExtract, which
		// converts between JSON types (5 read as "5", "1" and 1.5 as 1, 1 as true); found by the live run.
		{`city == "Oslo"`, "(" + city + " = ?)", []any{"Oslo"}},
		{`!(city == "Oslo")`, "(" + city + " != ? OR " + city + " IS NULL)", []any{"Oslo"}},
		{`city == null`, "((" + city + ") IS NULL)", nil},
		{`size(city) > 1`, "(COALESCE(lengthUTF8(" + city + "), 0) > ?)", []any{int64(1)}},
		{`first > 1`, "(if(JSONType(`profile`, 'scores', 1) IN ('Int64'), JSONExtract(`profile`, 'scores', 1, 'Nullable(Int64)'), NULL) > ?)", []any{int64(1)}},
		{`last < 1.5`, "(if(JSONType(`profile`, 'scores', -1) IN ('Int64', 'UInt64', 'Double'), JSONExtract(`profile`, 'scores', -1, 'Nullable(Float64)'), NULL) < ?)", []any{1.5}},
		{`vip`, "if(JSONType(`profile`, 'vip') IN ('Bool'), JSONExtract(`profile`, 'vip', 'Nullable(Bool)'), NULL)", nil},
		{`seen < timestamp("2026-01-01T00:00:00Z")`, "(parseDateTime64BestEffortOrNull(if(JSONType(`profile`, 'seen') IN ('String'), JSONExtract(`profile`, 'seen', 'Nullable(String)'), NULL), 3, 'UTC') < toDateTime64(?, 3, 'UTC'))", []any{"2026-01-01 00:00:00.000"}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.sql, sql)
			require.Equal(t, tc.args, args)
			require.Equal(t, len(args), strings.Count(sql, "?"))
		})
	}
}

func TestSchemaHasAndNil(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t)

	for _, tc := range []struct {
		field, sql, negated string
		args                []any
	}{
		{"note", "((`note`) IS NOT NULL)", "((`note`) IS NULL)", nil},
		{"name", "(`name` != ?)", "(`name` = ?)", []any{""}},
		{"tags", "notEmpty(`tags`)", "empty(`tags`)", nil},
		{"price", "(`price` != CAST(? AS Decimal(65, 30)))", "(`price` = CAST(? AS Decimal(65, 30)))", []any{"0"}},
		{"city", "((if(JSONType(`profile`, 'address', 'city') IN ('String'), JSONExtract(`profile`, 'address', 'city', 'Nullable(String)'), NULL)) IS NOT NULL)", "((if(JSONType(`profile`, 'address', 'city') IN ('String'), JSONExtract(`profile`, 'address', 'city', 'Nullable(String)'), NULL)) IS NULL)", nil},
	} {
		has := &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: tc.field}}
		sql, args, err := tr.Translate(has)
		require.NoError(t, err)
		require.Equal(t, tc.sql, sql)
		require.Equal(t, tc.args, args)

		sql, _, err = tr.Translate(&filter.UnaryOpNode{Op: filter.OpNot, Operand: has})
		require.NoError(t, err)
		require.Equal(t, tc.negated, sql)
	}

	sql, args, err := tr.Translate(nil)
	require.NoError(t, err)
	require.Equal(t, sqlbase.MatchAll, sql)
	require.Empty(t, args)
	require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, `name == "a"`)))
	require.ErrorIs(t, tr.ValidateAST(filtertest.MustParseFilter(t, `name > 1`)), filter.ErrFieldTypeMismatch)

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sql, args, err = tr.Translate(&filter.BinaryOpNode{Op: filter.OpIn, Left: &filter.IdentNode{Name: "times"},
		Right: &filter.ListNode{Elements: []filter.Node{&filter.LiteralNode{Value: at}}}})
	require.NoError(t, err)
	require.Equal(t, "hasAny(`times`, [toDateTime64(?, 3, 'UTC')])", sql)
	require.Equal(t, []any{"2026-01-02 03:04:05.000"}, args)
}

func TestSchemaRejects(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t)
	for _, tc := range []struct {
		expr string
		err  error
	}{
		{`name > 1`, filter.ErrFieldTypeMismatch},
		{`age == 1.5`, filter.ErrFieldTypeMismatch},
		{`name == age`, filter.ErrFieldTypeMismatch},
		{`age > null`, filter.ErrUnsupportedOperation},
		{`tags > name`, filter.ErrUnsupportedOperation},
		{`name in [note]`, filter.ErrUnsupportedOperation},
		{`"a" == tags`, filter.ErrUnsupportedOperation},
		{`name in [["a"]]`, filter.ErrUnsupportedOperation},
		{`name.contains(note)`, filter.ErrUnsupportedOperation},
		{`age.contains("a")`, filter.ErrFieldTypeMismatch},
		{`size(age) == 1`, filter.ErrFieldTypeMismatch},
		{`name`, filter.ErrInvalidExpression},
		{`tags`, filter.ErrInvalidExpression},
		{`!tags`, filter.ErrFieldTypeMismatch},
		{`age && done`, filter.ErrFieldTypeMismatch},
		{`name in "a"`, filter.ErrFieldTypeMismatch},
		{`doc == "a"`, filter.ErrUnsupportedOperation},
		{`uid == "a"`, filter.ErrUnsupportedOperation},
		{`amount > 1`, filter.ErrUnsupportedOperation},
		{`missing == 1`, filter.ErrInvalidOptions},
		{`price == 1e70`, filter.ErrFieldTypeMismatch},
		{`created > timestamp("1800-01-01T00:00:00Z")`, filter.ErrFieldTypeMismatch},
		{`created < timestamp("2300-01-01T00:00:00Z")`, filter.ErrFieldTypeMismatch},
		{`name.matches("é(?i)")`, filter.ErrUnsupportedOperation},
		{`name.matches("(?i)é")`, filter.ErrUnsupportedOperation},
		{`name.matches("(")`, filter.ErrInvalidRegex},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.ErrorIs(t, err, tc.err)
		})
	}

	_, _, err := tr.Translate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "a\x00"}})
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
	_, _, err = tr.Translate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "age"}, Right: &filter.LiteralNode{Value: uint64(1) << 63}})
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
	_, _, err = tr.Translate(&filter.UnaryOpNode{Op: filter.Operator(255), Operand: &filter.IdentNode{Name: "done"}})
	require.Error(t, err)
}

func TestSchemaDataBudget(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, clickhouse.WithSchemaMaxDataBudget(8))
	_, _, err := tr.Translate(filtertest.MustParseFilter(t, `name == "abc"`))
	require.NoError(t, err)
	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `name == "abcdefghij"`))
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
	_, _, err = tr.Translate(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "data"}, Right: &filter.LiteralNode{Value: []byte("abcdefghij")}})
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price == 1234567.891`))
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

func TestSchemaOptions(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text), "price": sqlbase.Scalar("price", sqlbase.Numeric),
		"created": sqlbase.Scalar("created", sqlbase.Timestamp)})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	plain, err := clickhouse.NewSchema(fields)
	require.NoError(t, err)

	schema := contractSchema(t, fields)

	for _, tc := range []struct {
		name   string
		schema *clickhouse.Schema
		opts   []clickhouse.FilterOption
	}{
		{name: "no contract mode", schema: plain},
		{name: "nil schema"},
		{name: "encoder", schema: schema, opts: []clickhouse.FilterOption{clickhouse.WithFilterLiteralEncoder(func(v any) (any, error) { return v, nil })}},
		{name: "renderer", schema: schema, opts: []clickhouse.FilterOption{clickhouse.WithFilterPredicateRenderer(func(filter.Node, sqlbase.ValueFunc) (string, bool, error) {
			return "", false, nil
		})}},
		{name: "scalar precision", schema: schema, opts: []clickhouse.FilterOption{clickhouse.WithFilterTimestampPrecision(clickhouse.DefaultTimestampPrecision)}},
		{name: "mapping conflict", schema: schema, opts: []clickhouse.FilterOption{clickhouse.WithFilterOptions(filter.WithFieldMapping(map[string]string{"name": "other"}))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := clickhouse.NewFilterTranslatorWithSchema(policy, tc.schema, tc.opts...)
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}

	for name, opts := range map[string][]clickhouse.SchemaOption{
		"negative precision": {clickhouse.WithSchemaTimestampPrecision(-time.Second)},
		"decimal format":     {clickhouse.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 0})},
	} {
		_, err = clickhouse.NewSchema(fields, opts...)
		require.ErrorIs(t, err, filter.ErrInvalidOptions, name)
	}

	_, err = clickhouse.NewSchema(nil)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	require.ErrorIs(t, (*clickhouse.Schema)(nil).ValidatePolicy(policy), filter.ErrInvalidOptions)

	_, err = clickhouse.NewFilterTranslatorWithSchema(nil, schema)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)

	tr, err := clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema(t, fields, clickhouse.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 38, Scale: 35}),
		clickhouse.WithSchemaTimestampPrecision(time.Microsecond)))
	require.NoError(t, err)

	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `price == "0.12345678901234567890123456789012345"`))
	require.NoError(t, err)
	require.Equal(t, "(`price` = CAST(? AS Decimal(38, 35)))", sql)
	require.Equal(t, []any{"0.12345678901234567890123456789012345"}, args)
	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price == "1000"`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)

	sql, args, err = tr.Translate(filtertest.MustParseFilter(t, `created == timestamp("2026-01-02T03:04:05.1234567Z")`))
	require.NoError(t, err)
	require.Equal(t, "(`created` = toDateTime64(?, 6, 'UTC'))", sql)
	require.Equal(t, []any{"2026-01-02 03:04:05.123456"}, args)

	tr, err = clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema(t, fields, clickhouse.WithSchemaTimestampPrecision(time.Second)))
	require.NoError(t, err)
	_, args, err = tr.Translate(filtertest.MustParseFilter(t, `created == timestamp("2026-01-02T03:04:05.9Z")`))
	require.NoError(t, err)
	require.Equal(t, []any{"2026-01-02 03:04:05"}, args)

	tr, err = clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema(t, fields, clickhouse.WithSchemaTimestampPrecision(0)))
	require.NoError(t, err)
	sql, args, err = tr.Translate(filtertest.MustParseFilter(t, `created == timestamp("2026-01-02T03:04:05.123456789Z")`))
	require.NoError(t, err)
	require.Equal(t, "(`created` = toDateTime64(?, 9, 'UTC'))", sql, "zero keeps nanoseconds")
	require.Equal(t, []any{"2026-01-02 03:04:05.123456789"}, args)
}

func TestSchemaPortablePolicy(t *testing.T) {
	t.Parallel()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name_col", sqlbase.Text), "price": sqlbase.Scalar("price", sqlbase.Numeric)})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithPortableContract(true), filter.WithAllowedFields("name", "price"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindString, "price": filter.FieldKindDecimal}))
	tr, err := clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema(t, schema),
		clickhouse.WithFilterOptions(filter.WithFieldMapping(map[string]string{"name": "name_col"})))
	require.NoError(t, err)

	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `name == "a" && price > 1.25`))
	require.NoError(t, err)
	require.Equal(t, "((`name_col` = ?) AND (`price` > CAST(? AS Decimal(65, 30))))", sql)
	require.Equal(t, []any{"a", "1.25"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `secret == "a"`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

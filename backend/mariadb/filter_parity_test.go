// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// parityPolicy declares array, decimal and string fields the way every filter translator sees them.
func parityPolicy(tb testing.TB) *filter.Policy {
	tb.Helper()

	return filtertest.MustPolicy(tb, filter.WithAnyField(true), filter.WithArrayFields("tags", "nums", "flags", "seen"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"tags": filter.FieldKindString, "nums": filter.FieldKindInt, "flags": filter.FieldKindBool,
			"seen": filter.FieldKindTimestamp, "price": filter.FieldKindDecimal, "name": filter.FieldKindString,
		}))
}

// TestTranslator_Parity pins array element semantics (WithArrayFields), exact decimal binding without an encoder and
// substring() in operand position.
func TestTranslator_Parity(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	tests := []struct {
		name, expr, want string
		args             []any
	}{
		{"array equality", `tags == "red"`, "JSON_CONTAINS(`tags`, JSON_ARRAY(?))", []any{"red"}},
		{"array equality reversed", `"red" == tags`, "JSON_CONTAINS(`tags`, JSON_ARRAY(?))", []any{"red"}},
		{"array not equal", `tags != "red"`, "(`tags` IS NULL OR NOT (JSON_CONTAINS(`tags`, JSON_ARRAY(?))))", []any{"red"}},
		{"array membership", `"red" in tags`, "JSON_CONTAINS(`tags`, JSON_ARRAY(?))", []any{"red"}},
		{"array in list", `tags in ["a", "b"]`, "((JSON_CONTAINS(`tags`, JSON_ARRAY(?))) OR (JSON_CONTAINS(`tags`, JSON_ARRAY(?))))", []any{"a", "b"}},
		{"array ordered", `nums > 3`, "EXISTS (SELECT 1 FROM JSON_TABLE(`nums`, '$[*]' COLUMNS (v BIGINT PATH '$')) AS _aip_e WHERE _aip_e.v > ?)", []any{int64(3)}},
		{"array ordered reversed", `3 < nums`, "EXISTS (SELECT 1 FROM JSON_TABLE(`nums`, '$[*]' COLUMNS (v BIGINT PATH '$')) AS _aip_e WHERE _aip_e.v > ?)", []any{int64(3)}},
		{"array ordered string", `tags >= "m"`, "EXISTS (SELECT 1 FROM JSON_TABLE(`tags`, '$[*]' COLUMNS (v LONGTEXT PATH '$')) AS _aip_e WHERE _aip_e.v >= ?)", []any{"m"}},
		{"array bool", `flags == true`, "JSON_CONTAINS(`flags`, 'true')", nil},
		{"array bool false", `flags != false`, "(`flags` IS NULL OR NOT (JSON_CONTAINS(`flags`, 'false')))", nil},
		{"array size", `tags.size() == 2`, "COALESCE(JSON_LENGTH(`tags`), 0) = ?", []any{int64(2)}},
		{"array contains", `tags.contains("x")`, "EXISTS (SELECT 1 FROM JSON_TABLE(`tags`, '$[*]' COLUMNS (v LONGTEXT PATH '$')) AS _aip_e WHERE LOCATE(?, _aip_e.v) > 0)", []any{"x"}},
		{"array null", `tags == null`, "`tags` IS NULL", nil},
		{"decimal", `price > 1.50`, "`price` > CAST(? AS DECIMAL(65,30))", []any{"1.5"}},
		{"decimal in", `price in [1.5, 2]`, "`price` IN (CAST(? AS DECIMAL(65,30)), CAST(? AS DECIMAL(65,30)))", []any{"1.5", "2"}},
		{"substring", `name.substring(0, 2) == "ab"`, "(CHAR_LENGTH(`name`) >= 2 AND SUBSTRING(`name`, 1, 2) = ?)", []any{"ab"}},
		{"substring to end", `name.substring(1) == "b"`, "(CHAR_LENGTH(`name`) >= 1 AND SUBSTRING(`name`, 2) = ?)", []any{"b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, args, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslator_ParityInline(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	for expr, want := range map[string]string{
		`tags == "red"`: "JSON_CONTAINS(`tags`, JSON_ARRAY('red'))",
		`price > 1.5`:   "`price` > CAST('1.5' AS DECIMAL(65,30))",
	} {
		got, err := tr.TranslateInline(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

func TestTranslator_ParityErrors(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	for expr, want := range map[string]error{
		`tags == name`:                              filter.ErrUnsupportedOperation,
		`name == tags`:                              filter.ErrUnsupportedOperation,
		`tags`:                                      filter.ErrUnsupportedOperation,
		`name in tags`:                              filter.ErrUnsupportedOperation,
		`tags.contains(name)`:                       filter.ErrUnsupportedOperation,
		`tags == 1`:                                 filter.ErrFieldTypeMismatch,
		`name.substring(2, 1) == "a"`:               filter.ErrInvalidExpression,
		`name.substring(-1) == "a"`:                 filter.ErrInvalidExpression,
		`name.substring(0, 1.5) == "a"`:             filter.ErrInvalidExpression,
		`tags.substring(0, 1) == "a"`:               filter.ErrUnsupportedOperation,
		`name.substring(0, age) == "a"`:             filter.ErrUnsupportedOperation,
		`name.substring(0, 1, 2) == "a"`:            filter.ErrInvalidExpression,
		`seen == timestamp("2024-01-01T00:00:00Z")`: filter.ErrUnsupportedType,
		`flags > true`:                              filter.ErrUnsupportedOperation,
		`seen > timestamp("2024-01-01T00:00:00Z")`:  filter.ErrUnsupportedType,
	} {
		_, _, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, want, expr)
	}
}

func TestWithPredicateRenderer(t *testing.T) {
	t.Parallel()

	render := func(n filter.Node, value sqlbase.ValueFunc) (string, bool, error) {
		if id, ok := n.(*filter.IdentNode); ok && id.Name == "custom" {
			arg, err := value("x")
			return "custom(" + arg + ")", true, err
		}

		return "", false, nil
	}

	tr, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("custom", "name")), mariadb.WithFilterPredicateRenderer(render))
	require.NoError(t, err)

	got, args, err := tr.Translate(filtertest.MustParseFilter(t, `custom && name == "a"`))
	require.NoError(t, err)
	require.Equal(t, "(custom(?)) AND (`name` = ?)", got)
	require.Equal(t, []any{"x", "a"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `secret`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed, "authorization runs before the renderer")
}

func TestTranslator_ParityMariaDBLimits(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(parityPolicy(t), mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 10, Scale: 2}))
	require.NoError(t, err)

	got, args, err := tr.Translate(filtertest.MustParseFilter(t, `price < 9.99`))
	require.NoError(t, err)
	require.Equal(t, "`price` < CAST(? AS DECIMAL(10,2))", got)
	require.Equal(t, []any{"9.99"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price < 9.999`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch, "a value the cast would round")

	_, err = mariadb.NewFilterTranslator(parityPolicy(t), mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 70}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)

	_, err = mariadb.NewFilterTranslatorWithSchema(parityPolicy(t), nil, mariadb.WithFilterPredicateRenderer(
		func(filter.Node, sqlbase.ValueFunc) (string, bool, error) { return "", false, nil }))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

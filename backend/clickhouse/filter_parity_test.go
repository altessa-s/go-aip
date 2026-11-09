// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
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

	tr, err := clickhouse.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	tests := []struct {
		name, expr, want string
		args             []any
	}{
		{"array equality", `tags == "red"`, "has(`tags`, ?)", []any{"red"}},
		{"array equality reversed", `"red" == tags`, "has(`tags`, ?)", []any{"red"}},
		{"array not equal", `tags != "red"`, "NOT (has(`tags`, ?))", []any{"red"}},
		{"array membership", `"red" in tags`, "has(`tags`, ?)", []any{"red"}},
		{"array in list", `tags in ["a", "b"]`, "((has(`tags`, ?)) OR (has(`tags`, ?)))", []any{"a", "b"}},
		{"array ordered", `nums > 3`, "arrayExists(_aip_e -> _aip_e > ?, `nums`)", []any{int64(3)}},
		{"array ordered reversed", `3 < nums`, "arrayExists(_aip_e -> _aip_e > ?, `nums`)", []any{int64(3)}},
		{"array bool", `flags == true`, "has(`flags`, ?)", []any{true}},
		{"array size", `tags.size() == 2`, "length(`tags`) = ?", []any{int64(2)}},
		{"array contains", `tags.contains("x")`, "arrayExists(_aip_e -> position(_aip_e, ?) > 0, `tags`)", []any{"x"}},
		{"array null", `tags == null`, "empty(`tags`)", nil},
		{"array not null", `tags != null`, "notEmpty(`tags`)", nil},
		{"decimal", `price > 1.50`, "`price` > toDecimal128(?, 1)", []any{"1.5"}},
		{"decimal integer", `price == 2`, "`price` = toDecimal128(?, 0)", []any{"2"}},
		{"decimal in", `price in [1.25, 2]`, "`price` IN (toDecimal128(?, 2), toDecimal128(?, 0))", []any{"1.25", "2"}},
		{"substring", `name.substring(0, 2) == "ab"`, "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 2 AND substringUTF8(`name`, 1, 2) = ?)", []any{"ab"}},
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

// TestTranslator_ArrayExists pins has() of an Array column, which cannot be NULL: an absent array is stored empty.
func TestTranslator_ArrayExists(t *testing.T) {
	t.Parallel()

	tr, err := clickhouse.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	got, args, err := tr.Translate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "tags"}})
	require.NoError(t, err)
	require.Equal(t, "notEmpty(`tags`)", got)
	require.Empty(t, args)
}

func TestTranslator_ParityInline(t *testing.T) {
	t.Parallel()

	tr, err := clickhouse.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	for expr, want := range map[string]string{
		`tags == "red"`: "has(`tags`, 'red')",
		`price > 1.5`:   "`price` > toDecimal128('1.5', 1)",
	} {
		got, err := tr.TranslateInline(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

func TestTranslator_ParityErrors(t *testing.T) {
	t.Parallel()

	tr, err := clickhouse.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	for expr, want := range map[string]error{
		`tags == name`:                   filter.ErrUnsupportedOperation,
		`name == tags`:                   filter.ErrUnsupportedOperation,
		`tags`:                           filter.ErrUnsupportedOperation,
		`name in tags`:                   filter.ErrUnsupportedOperation,
		`tags.contains(name)`:            filter.ErrUnsupportedOperation,
		`tags == 1`:                      filter.ErrFieldTypeMismatch,
		`name.substring(2, 1) == "a"`:    filter.ErrInvalidExpression,
		`name.substring(-1) == "a"`:      filter.ErrInvalidExpression,
		`name.substring(0, 1.5) == "a"`:  filter.ErrInvalidExpression,
		`tags.substring(0, 1) == "a"`:    filter.ErrUnsupportedOperation,
		`name.substring(0, age) == "a"`:  filter.ErrUnsupportedOperation,
		`name.substring(0, 1, 2) == "a"`: filter.ErrInvalidExpression,
	} {
		_, _, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, want, expr)
	}
}

func TestWithFilterPredicateRenderer(t *testing.T) {
	t.Parallel()

	render := func(n filter.Node, value sqlbase.ValueFunc) (string, bool, error) {
		if id, ok := n.(*filter.IdentNode); ok && id.Name == "custom" {
			arg, err := value("x")
			return "custom(" + arg + ")", true, err
		}

		return "", false, nil
	}

	tr, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("custom", "name")), clickhouse.WithFilterPredicateRenderer(render))
	require.NoError(t, err)

	got, args, err := tr.Translate(filtertest.MustParseFilter(t, `custom && name == "a"`))
	require.NoError(t, err)
	require.Equal(t, "(custom(?)) AND (`name` = ?)", got)
	require.Equal(t, []any{"x", "a"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `secret`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed, "authorization runs before the renderer")
}

func TestTranslator_ParityClickHouseDecimalRange(t *testing.T) {
	t.Parallel()

	tr, err := clickhouse.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price < 123456789012345678901234567890123456789.5`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}

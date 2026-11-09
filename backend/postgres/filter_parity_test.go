// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
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

	tr, err := postgres.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	tests := []struct {
		name, expr, want string
		args             []any
	}{
		{"array equality", `tags == "red"`, `$1 = ANY("tags")`, []any{"red"}},
		{"array equality reversed", `"red" == tags`, `$1 = ANY("tags")`, []any{"red"}},
		{"array not equal", `tags != "red"`, `("tags" IS NULL OR NOT ($1 = ANY("tags")))`, []any{"red"}},
		{"array membership", `"red" in tags`, `$1 = ANY("tags")`, []any{"red"}},
		{"array in list", `tags in ["a", "b"]`, `(($1 = ANY("tags")) OR ($2 = ANY("tags")))`, []any{"a", "b"}},
		{"array in single", `tags in ["a"]`, `($1 = ANY("tags"))`, []any{"a"}},
		{"array in empty", `tags in []`, `1 = 0`, nil},
		{"array ordered", `nums > 3`, `$1 < ANY("nums")`, []any{int64(3)}},
		{"array ordered reversed", `3 < nums`, `$1 < ANY("nums")`, []any{int64(3)}},
		{"array bool", `flags == true`, `$1 = ANY("flags")`, []any{true}},
		{"array size", `tags.size() == 2`, `COALESCE(cardinality("tags"), 0) = $1`, []any{int64(2)}},
		{"array size global", `size(tags) > 0`, `COALESCE(cardinality("tags"), 0) > $1`, []any{int64(0)}},
		{"array contains", `tags.contains("x")`, `EXISTS (SELECT 1 FROM unnest("tags") AS _aip_e(v) WHERE strpos(_aip_e.v, $1) > 0)`, []any{"x"}},
		{"array endsWith", `tags.endsWith("x")`, `EXISTS (SELECT 1 FROM unnest("tags") AS _aip_e(v) WHERE right(_aip_e.v, length($1)) = $2)`, []any{"x", "x"}},
		{"array null", `tags == null`, `"tags" IS NULL`, nil},
		{"array negated", `!(tags == "a")`, `("tags" IS NULL OR NOT ($1 = ANY("tags")))`, []any{"a"}},
		{"decimal", `price > 1.50`, `"price" > CAST($1 AS NUMERIC)`, []any{"1.5"}},
		{"decimal reversed", `1.5 <= price`, `CAST($1 AS NUMERIC) <= "price"`, []any{"1.5"}},
		{"decimal in", `price in [1.5, 2]`, `"price" IN (CAST($1 AS NUMERIC), CAST($2 AS NUMERIC))`, []any{"1.5", "2"}},
		{"substring", `name.substring(0, 2) == "ab"`, `(length("name") >= 2 AND substr("name", 1, 2) = $1)`, []any{"ab"}},
		{"substring to end", `name.substring(1) == "b"`, `(length("name") >= 1 AND substr("name", 2) = $1)`, []any{"b"}},
		{"substring from zero", `name.substring(0) == "b"`, `substr("name", 1) = $1`, []any{"b"}},
		{"substring in", `name.substring(1, 3) in ["ab"]`, `(length("name") >= 3 AND substr("name", 2, 2) IN ($1))`, []any{"ab"}},
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

	tr, err := postgres.NewFilterTranslator(parityPolicy(t))
	require.NoError(t, err)

	for expr, want := range map[string]string{
		`tags == "red"`:                `E'red' = ANY("tags")`,
		`price > 1.5`:                  `"price" > CAST(E'1.5' AS NUMERIC)`,
		`name.substring(0, 2) == "ab"`: `(length("name") >= 2 AND substr("name", 1, 2) = E'ab')`,
	} {
		got, err := tr.TranslateInline(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

func TestTranslator_ParityErrors(t *testing.T) {
	t.Parallel()

	tr, err := postgres.NewFilterTranslator(parityPolicy(t))
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

	tr, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("custom", "name")), postgres.WithFilterPredicateRenderer(render))
	require.NoError(t, err)

	got, args, err := tr.Translate(filtertest.MustParseFilter(t, `custom && name == "a"`))
	require.NoError(t, err)
	require.Equal(t, `(custom($1)) AND ("name" = $2)`, got)
	require.Equal(t, []any{"x", "a"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `secret`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed, "authorization runs before the renderer")
}

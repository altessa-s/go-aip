// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

// extended is a dialect implementing every optional extension with readable markers; errOn makes one of them fail.
type extended struct {
	failing

	errOn string
}

func (d extended) ArrayCompare(col string, op filter.Operator, operand any, _ filter.FieldKind, value sqlbase.ValueFunc) (string, error) {
	if d.errOn == "compare" {
		return "", errDialect
	}

	sqlOp, err := sqlbase.ComparisonOperator(op)
	if err != nil {
		return "", err
	}

	arg, err := value(operand)

	return "any(" + col + " " + sqlOp + " " + arg + ")", err
}

func (d extended) ArrayStringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	pred, err := d.StringPredicate(op, "e", needle, value)
	return "exists(" + col + ": " + pred + ")", err
}

func (extended) ArraySize(col string) string { return "count(" + col + ")" }

func (d extended) DecimalValue(v decimal.Decimal, value sqlbase.ValueFunc) (string, error) {
	if d.errOn == "decimal" {
		return "", errDialect
	}

	arg, err := value(v.String())

	return "dec(" + arg + ")", err
}

func (d extended) TimestampValue(ts time.Time, value sqlbase.ValueFunc) (string, error) {
	if d.errOn == "timestamp" {
		return "", errDialect
	}

	arg, err := value(ts.UTC().Format(time.RFC3339Nano))

	return "ts(" + arg + ")", err
}

func (extended) SubstringExpr(col string, start, length int64) string {
	return "sub(" + col + ", " + strconv.FormatInt(start, 10) + ", " + strconv.FormatInt(length, 10) + ")"
}

func extendedPolicy(tb testing.TB) *filter.Policy {
	tb.Helper()

	return filtertest.MustPolicy(tb, filter.WithAnyField(true), filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal, "nums": filter.FieldKindDecimal}),
		filter.WithArrayFields("tags", "nums"))
}

func TestExtensions(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(extendedPolicy(t), extended{})
	require.NoError(t, err)

	tests := []struct {
		expr, want string
		args       []any
	}{
		{`tags == "a"`, `any("tags" = ?)`, []any{"a"}},
		{`"a" <= tags`, `any("tags" >= ?)`, []any{"a"}},
		{`tags != "a"`, `("tags" IS NULL OR NOT (any("tags" = ?)))`, []any{"a"}},
		{`"a" in tags`, `any("tags" = ?)`, []any{"a"}},
		{`tags in ["a", "b"]`, `((any("tags" = ?)) OR (any("tags" = ?)))`, []any{"a", "b"}},
		{`tags in []`, `1 = 0`, nil},
		{`nums > 1.5`, `any("nums" > dec(?))`, []any{"1.5"}},
		{`size(tags) == 0`, `count("tags") = ?`, []any{int64(0)}},
		{`tags.startsWith("a")`, `exists("tags": e ~ ?)`, []any{"a"}},
		{`price == 2.50`, `"price" = dec(?)`, []any{"2.5"}},
		{`created < timestamp("2022-12-31T23:59:59.5Z")`, `"created" < ts(?)`, []any{"2022-12-31T23:59:59.5Z"}},
		{`created in [timestamp("2024-01-01T02:00:00+02:00")]`, `"created" IN (ts(?))`, []any{"2024-01-01T00:00:00Z"}},
		{`name.substring(1, 3) == "bc"`, `(length("name") >= 3 AND sub("name", 1, 2) = ?)`, []any{"bc"}},
		{`"bc" == name.substring(1)`, `(length("name") >= 1 AND ? = sub("name", 1, -1))`, []any{"bc"}},
		{`name.substring(0, 2) in ["ab", "cd"]`, `(length("name") >= 2 AND sub("name", 0, 2) IN (?, ?))`, []any{"ab", "cd"}},
		{`name.substring(0, 2) in [other]`, `(length("name") >= 2 AND (((length("name") >= 2 AND sub("name", 0, 2) = "other"))))`, nil},
		{`name.substring(0, 2) in []`, `1 = 0`, nil},
		{`name.substring(0) == null`, `1 = 0`, nil},
		{`name.substring(1) != null`, `(length("name") >= 1 AND sub("name", 1, -1) IS NOT NULL)`, nil},
		{`!(name.substring(1) == "b")`, `(length("name") >= 1 AND sub("name", 1, -1) != ?)`, []any{"b"}},
		{`!(name.substring(1) < "b")`, `(length("name") >= 1 AND NOT (sub("name", 1, -1) < ?))`, []any{"b"}},
		{`!(name.substring(1) in [])`, `(length("name") >= 1 AND sub("name", 1, -1) IS NOT NULL)`, nil},
		{`!(name.substring(1) in ["b", null])`, `(length("name") >= 1 AND sub("name", 1, -1) NOT IN (?))`, []any{"b"}},
		{`!(nums > 1.5)`, `("nums" IS NULL OR NOT (any("nums" > dec(?))))`, []any{"1.5"}},
		{`!tags.startsWith("a")`, `("tags" IS NULL OR NOT (exists("tags": e ~ ?)))`, []any{"a"}},
	}

	for _, tt := range tests {
		got, args, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		require.Equal(t, tt.want, got, tt.expr)
		require.Equal(t, tt.args, args, tt.expr)
	}
}

func TestExtensionsErrors(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(extendedPolicy(t), extended{errOn: "compare"})
	require.NoError(t, err)

	for _, expr := range []string{`tags == "a"`, `tags != "a"`} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, errDialect, expr)
	}

	tr, err = sqlbase.NewFilterTranslator(extendedPolicy(t), extended{errOn: "decimal"})
	require.NoError(t, err)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `price == 1.5`))
	require.ErrorIs(t, err, errDialect)

	tr, err = sqlbase.NewFilterTranslator(extendedPolicy(t), extended{errOn: "timestamp"})
	require.NoError(t, err)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `created == timestamp("2024-01-01T00:00:00Z")`))
	require.ErrorIs(t, err, errDialect)

	inline, err := tr.TranslateInline(filtertest.MustParseFilter(t, `created == timestamp("2024-01-01T00:00:00Z")`))
	require.NoError(t, err, "the inline form keeps FormatLiteral")
	require.NotContains(t, inline, "ts(")

	tr, err = sqlbase.NewFilterTranslator(extendedPolicy(t), extended{})
	require.NoError(t, err)

	for expr, want := range map[string]error{
		`tags == other`:             filter.ErrUnsupportedOperation,
		`tags`:                      filter.ErrUnsupportedOperation,
		`other in tags`:             filter.ErrUnsupportedOperation,
		`tags.endsWith(other)`:      filter.ErrUnsupportedOperation,
		`tags.matches("(")`:         regexanchor.ErrInvalidRegex,
		`tags.contains(1)`:          filter.ErrInvalidExpression,
		`tags in ["a", other]`:      filter.ErrUnsupportedOperation,
		`price == "x"`:              filter.ErrFieldTypeMismatch,
		`name.substring(0, 1) == 1`: nil,
	} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		if want == nil {
			require.NoError(t, err, expr)
			continue
		}

		require.ErrorIs(t, err, want, expr)
	}

	// A hand-built comparison with an unsupported operator is rejected before rendering.
	_, _, err = tr.Translate(&filter.BinaryOpNode{Op: filter.OpContains, Left: &filter.IdentNode{Name: "tags"},
		Right: &filter.LiteralNode{Value: "a"}})
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestConverse(t *testing.T) {
	t.Parallel()

	for op, want := range map[filter.Operator]filter.Operator{
		filter.OpLT: filter.OpGT, filter.OpLTE: filter.OpGTE, filter.OpGT: filter.OpLT, filter.OpGTE: filter.OpLTE,
		filter.OpEqual: filter.OpEqual, filter.OpNotEqual: filter.OpNotEqual,
	} {
		require.Equal(t, want, sqlbase.Converse(op))
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// mustTranslator builds a translator and fails the test on any
// construction error. Keeps the success-path tests free of
// error-wiring noise; tests that exercise construction failures call
// [NewFilterTranslator] directly.
func mustTranslator(tb testing.TB, opts ...any) *FilterTranslator {
	tb.Helper()

	policy, binding := anyField(tb, opts)
	tr, err := NewFilterTranslator(policy, binding)
	require.NoError(tb, err)

	return tr
}

// anyField binds the shared test policy through this backend's filter options.
func anyField(tb testing.TB, opts []any) (*filter.Policy, FilterOption) {
	tb.Helper()

	policy, binding := filtertest.AnyFieldPolicy(tb, opts...)

	return policy, WithFilterOptions(binding...)
}

func TestTranslator_BasicComparisons(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{"equal string", `name == "John"`, "`name` = ?", []any{"John"}},
		{"equal int", `age == 25`, "`age` = ?", []any{int64(25)}},
		{"equal bool", `active == true`, "`active` = ?", []any{true}},
		{"equal float", `price == 10.5`, "`price` = ?", []any{10.5}},
		{"not equal", `status != "deleted"`, "(`status` != ? OR `status` IS NULL)", []any{"deleted"}},
		{"negated equal", `!(name == "John")`, "(`name` != ? OR `name` IS NULL)", []any{"John"}},
		{"field not equal field", `name != status`, "COALESCE(`name` != `status`, (`name` IS NULL) != (`status` IS NULL))", nil},
		{"greater than", `age > 18`, "`age` > ?", []any{int64(18)}},
		{"greater or equal", `age >= 18`, "`age` >= ?", []any{int64(18)}},
		{"less than", `age < 65`, "`age` < ?", []any{int64(65)}},
		{"less or equal", `age <= 65`, "`age` <= ?", []any{int64(65)}},
		{"reversed operands", `18 < age`, "? < `age`", []any{int64(18)}},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslator_NullComparisons(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"equal null", `deletedAt == null`, "`deletedAt` IS NULL"},
		{"not equal null", `deletedAt != null`, "`deletedAt` IS NOT NULL"},
		{"null on the left", `null == deletedAt`, "`deletedAt` IS NULL"},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Empty(t, args)
		})
	}
}

func TestTranslator_NullOrderingRejected(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `deletedAt > null`)

	_, _, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestTranslator_LogicalOperators(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"and",
			`name == "John" && age >= 18`,
			"(`name` = ?) AND (`age` >= ?)",
			[]any{"John", int64(18)},
		},
		{
			"or",
			`status == "active" || status == "pending"`,
			"(`status` = ?) OR (`status` = ?)",
			[]any{"active", "pending"},
		},
		{
			"not on bare ident",
			`!active`,
			"NOT (`active` = true)",
			nil,
		},
		{
			"not on expression",
			`!(age >= 18)`,
			"NOT (`age` >= ?)",
			[]any{int64(18)},
		},
		{
			"chained and preserves argument order",
			`a == 1 && b == 2 && c == 3`,
			"((`a` = ?) AND (`b` = ?)) AND (`c` = ?)",
			[]any{int64(1), int64(2), int64(3)},
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslator_BareIdentIsBooleanTest(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `active && verified`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "(`active` = true) AND (`verified` = true)", got)
	require.Empty(t, args)
}

func TestTranslator_In(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"strings",
			`status in ["active", "pending"]`,
			"`status` IN (?, ?)",
			[]any{"active", "pending"},
		},
		{
			"ints",
			`type in [1, 2, 3]`,
			"`type` IN (?, ?, ?)",
			[]any{int64(1), int64(2), int64(3)},
		},
		{
			"single element",
			`status in ["active"]`,
			"`status` IN (?)",
			[]any{"active"},
		},
		{
			"empty list is match-none",
			`status in []`,
			sqlbase.MatchNone,
			nil,
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

// TestTranslator_EmptyInStillChecksAllowlist guards the rollback path in
// translateIn: the left operand is rendered — and therefore validated —
// before the empty list short-circuits to a constant.
func TestTranslator_EmptyInStillChecksAllowlist(t *testing.T) {
	trans := mustTranslator(t, filter.WithAllowedFields("name"))
	node := filtertest.MustParseFilter(t, `status in []`)

	_, _, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestTranslator_StringFunctions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"contains",
			`name.contains("oh")`,
			"position(`name`, ?) > 0",
			[]any{"oh"},
		},
		{
			"contains keeps LIKE wildcards literal",
			`name.contains("100%_x")`,
			"position(`name`, ?) > 0",
			[]any{"100%_x"},
		},
		{
			"startsWith",
			`name.startsWith("Jo")`,
			"startsWith(`name`, ?)",
			[]any{"Jo"},
		},
		{
			"endsWith",
			`name.endsWith("hn")`,
			"endsWith(`name`, ?)",
			[]any{"hn"},
		},
		{
			"matches",
			`name.matches("^Jo.*n$")`,
			"match(`name`, ?)",
			[]any{"^Jo.*n$"},
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslator_MatchesRejectsOversizedPattern(t *testing.T) {
	trans := mustTranslator(t, filter.WithMaxRegexLength(8))
	node := filtertest.MustParseFilter(t, `name.matches("aaaaaaaaaaaaaaaaaaaa")`)

	_, _, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrInvalidRegex)
}

func TestTranslator_Has(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `has(user.email)`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "`user.email` IS NOT NULL", got)
	require.Empty(t, args)
}

func TestTranslator_Size(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"method form",
			`tags.size() > 2`,
			"if(startsWith(toTypeName(`tags`), 'Array('), length(`tags`), lengthUTF8(toString(`tags`))) > ?",
			[]any{int64(2)},
		},
		{
			"function form",
			`size(tags) == 0`,
			"if(startsWith(toTypeName(`tags`), 'Array('), length(`tags`), lengthUTF8(toString(`tags`))) = ?",
			[]any{int64(0)},
		},
		{
			"on the right-hand side",
			`3 <= tags.size()`,
			"? <= if(startsWith(toTypeName(`tags`), 'Array('), length(`tags`), lengthUTF8(toString(`tags`)))",
			[]any{int64(3)},
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, args, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslator_UnsupportedOperations(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"bare size", `tags.size()`},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			_, _, err := trans.Translate(node)
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
		})
	}
}

func TestTranslator_NestedFields(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"two levels", `address.city == "NYC"`, "`address.city` = ?"},
		{"three levels", `user.profile.name == "John"`, "`user.profile.name` = ?"},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, _, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_FieldMapping(t *testing.T) {
	trans := mustTranslator(t, filter.WithFieldMapping(map[string]string{
		"organizationIds": "organization_id",
		"createdAt":       "created_at",
	}))
	node := filtertest.MustParseFilter(t, `organizationIds in ["org-1"] && createdAt > 100`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "(`organization_id` IN (?)) AND (`created_at` > ?)", got)
	require.Equal(t, []any{"org-1", int64(100)}, args)
}

// TestTranslator_FieldMappingCannotEscapeColumnPosition pins the reason
// mapped names are validated rather than merely quoted: the column
// position is the one part of the clause a placeholder cannot cover.
func TestTranslator_FieldMappingCannotEscapeColumnPosition(t *testing.T) {
	tests := []struct {
		name   string
		mapped string
	}{
		{"backtick", "name` = '' OR 1 = 1 -- "},
		{"expression", "lower(name)"},
		{"map subscript", "metadata['x']"},
		{"empty", ""},
		{"empty segment", "address..city"},
		{"leading digit", "1name"},
		{"whitespace", "name OR 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans := mustTranslator(t, filter.WithFieldMapping(map[string]string{"name": tt.mapped}))
			node := filtertest.MustParseFilter(t, `name == "x"`)

			_, _, err := trans.Translate(node)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
		})
	}
}

func TestTranslator_AllowedFields(t *testing.T) {
	trans := mustTranslator(t, filter.WithAllowedFields("name", "age"))

	t.Run("allowed", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name == "John" && age > 18`)
		_, _, err := trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("denied", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `passwordHash > ""`)
		_, _, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	})
}

func TestNewFilterTranslator_UntrustedInputRequiresAllowlist(t *testing.T) {
	t.Run("without allowlist", func(t *testing.T) {
		_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
		require.ErrorIs(t, err, filter.ErrAllowlistRequired)
	})

	t.Run("with allowlist", func(t *testing.T) {
		_, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")))
		require.NoError(t, err)
	})
}

func TestTranslator_MaxDepth(t *testing.T) {
	trans := mustTranslator(t, filter.WithMaxDepth(2))
	node := filtertest.MustParseFilter(t, `a == 1 && b == 2 && c == 3 && d == 4`)

	_, _, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
}

func TestTranslator_NilNodeMatchesAll(t *testing.T) {
	trans := mustTranslator(t)

	where, args, err := trans.Translate(nil)
	require.NoError(t, err)
	require.Equal(t, sqlbase.MatchAll, where)
	require.Empty(t, args)

	inline, err := trans.TranslateInline(nil)
	require.NoError(t, err)
	require.Equal(t, sqlbase.MatchAll, inline)
}

// TestTranslator_ArgsAreNotRetained guards the state reset in run: a
// second Translate must not inherit the previous call's arguments, and
// the slice handed to the first caller must not be reused underneath it.
func TestTranslator_ArgsAreNotRetained(t *testing.T) {
	trans := mustTranslator(t)

	first := filtertest.MustParseFilter(t, `a == 1 && b == 2`)
	_, firstArgs, err := trans.Translate(first)
	require.NoError(t, err)
	require.Equal(t, []any{int64(1), int64(2)}, firstArgs)

	second := filtertest.MustParseFilter(t, `c == 3`)
	_, secondArgs, err := trans.Translate(second)
	require.NoError(t, err)
	require.Equal(t, []any{int64(3)}, secondArgs)
	require.Equal(t, []any{int64(1), int64(2)}, firstArgs, "first result was mutated by the second call")
}

// TestTranslator_ArgsClearedAfterError keeps a failed translation from
// leaking half-collected arguments into the next call.
func TestTranslator_ArgsClearedAfterError(t *testing.T) {
	trans := mustTranslator(t, filter.WithAllowedFields("a"))

	failing := filtertest.MustParseFilter(t, `a == 1 && forbidden == 2`)
	_, args, err := trans.Translate(failing)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	require.Nil(t, args)

	ok := filtertest.MustParseFilter(t, `a == 9`)
	_, args, err = trans.Translate(ok)
	require.NoError(t, err)
	require.Equal(t, []any{int64(9)}, args)
}

// TestTranslator_Timestamp checks a bound timestamp keeps every fractional digit: clickhouse-go formats a positional
// time.Time to whole seconds, so `created < timestamp("…59.5Z")` missed a row stored at …59 until the operand was bound
// as text converted by toDateTime64.
func TestTranslator_Timestamp(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t)
	for _, tc := range []struct {
		expr, sql, arg string
	}{
		{`createdAt > timestamp("2024-01-02T03:04:05.123Z")`, "`createdAt` > toDateTime64(?, 3, 'UTC')", "2024-01-02 03:04:05.123"},
		{`createdAt < timestamp("2022-12-31T23:59:59.5Z")`, "`createdAt` < toDateTime64(?, 1, 'UTC')", "2022-12-31 23:59:59.5"},
		{`createdAt == timestamp("2024-01-02T05:04:05+02:00")`, "`createdAt` = toDateTime64(?, 0, 'UTC')", "2024-01-02 03:04:05"},
		{`createdAt >= timestamp("2024-01-02T03:04:05.000000001Z")`, "`createdAt` >= toDateTime64(?, 9, 'UTC')", "2024-01-02 03:04:05.000000001"},
		{`createdAt in [timestamp("2299-12-31T23:59:59.12345678Z")]`, "`createdAt` IN (toDateTime64(?, 8, 'UTC'))", "2299-12-31 23:59:59.12345678"},
	} {
		got, args, err := trans.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		require.Equal(t, tc.sql, got, tc.expr)
		require.Equal(t, []any{tc.arg}, args, tc.expr)
	}

	for _, expr := range []string{
		`createdAt > timestamp("1899-12-31T23:59:59Z")`,
		`createdAt > timestamp("2300-01-01T00:00:00Z")`,
		`createdAt > timestamp("2263-01-01T00:00:00.000000001Z")`,
	} {
		_, _, err := trans.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrFieldTypeMismatch, expr)
	}
}

func TestTranslateInline(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"string", `name == "John"`, "`name` = 'John'"},
		{"int", `age == 25`, "`age` = 25"},
		{"float", `price >= 10.5`, "`price` >= 10.5"},
		{"bool true", `active == true`, "`active` = true"},
		{"bool false", `active == false`, "`active` = false"},
		{"null", `deletedAt == null`, "`deletedAt` IS NULL"},
		{"in list", `status in ["a", "b"]`, "`status` IN ('a', 'b')"},
		{"contains", `name.contains("oh")`, "position(`name`, 'oh') > 0"},
		{"matches", `name.matches("^Jo")`, "match(`name`, '^Jo')"},
		{"size", `tags.size() > 2`, "if(startsWith(toTypeName(`tags`), 'Array('), length(`tags`), lengthUTF8(toString(`tags`))) > 2"},
		{
			"timestamp",
			`createdAt > timestamp("2024-01-02T03:04:05.123Z")`,
			"`createdAt` > toDateTime64('2024-01-02 03:04:05.123', 3, 'UTC')",
		},
		{
			"logical",
			`name == "John" && age >= 18`,
			"(`name` = 'John') AND (`age` >= 18)",
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.TranslateInline(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslateInline_QuotesHostileLiterals covers the escaping that
// inline mode leans on, since it has no placeholder to hide behind.
func TestTranslateInline_QuotesHostileLiterals(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"single quote", `O'Brien`, `'O\'Brien'`},
		{"backslash", `a\b`, `'a\\b'`},
		{"quote break-out attempt", `' OR 1=1 --`, `'\' OR 1=1 --'`},
		{"escaped quote break-out attempt", `\' OR 1=1 --`, `'\\\' OR 1=1 --'`},
		{"newline", "a\nb", `'a\nb'`},
		{"tab", "a\tb", `'a\tb'`},
		{"carriage return", "a\rb", `'a\rb'`},
		{"nul", "a\x00b", `'a\0b'`},
		{"multi-byte rune", "Ünïcödé", `'\xc3\x9cn\xc3\xafc\xc3\xb6d\xc3\xa9'`},
		{"multibyte breakout", "丿\\' OR 1=1 -- ", `'\xe4\xb8\xbf\\\' OR 1=1 -- '`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteString(tt.value))
		})
	}
}

func TestFormatLiteral(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "NULL"},
		{"uint", uint64(7), "7"},
		{"negative int", int64(-7), "-7"},
		{"nan", math.NaN(), "nan"},
		{"positive infinity", math.Inf(1), "inf"},
		{"negative infinity", math.Inf(-1), "-inf"},
		{"bytes", []byte{0xde, 0xad}, "unhex('dead')"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterDialect{}.FormatLiteral(tt.value)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		_, err := filterDialect{}.FormatLiteral(struct{}{})
		require.ErrorIs(t, err, filter.ErrUnsupportedType)
	})
}

func TestTimestampPrecision(t *testing.T) {
	t.Parallel()

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "ts"}, Right: &filter.LiteralNode{Value: time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)}}

	for precision, want := range map[time.Duration]string{
		time.Microsecond: "'2026-01-01 00:00:00.123456', 6, 'UTC')",
		time.Second:      "'2026-01-01 00:00:00', 0, 'UTC')",
		0:                "'2026-01-01 00:00:00.123456789', 9, 'UTC')",
	} {
		tr, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), WithFilterTimestampPrecision(precision))
		require.NoError(t, err)

		out, err := tr.TranslateInline(n)
		require.NoError(t, err)
		require.Contains(t, out, want)
	}

	_, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), WithFilterTimestampPrecision(-time.Second))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

// TestTranslateInline_SubstringBoundsDoNotOverflow pins the bound arithmetic of substring(): the largest start is capped
// rather than wrapped to a negative position, and an end that wraps on subtraction is rejected.
func TestTranslateInline_SubstringBoundsDoNotOverflow(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t)

	for expr, want := range map[string]string{
		`name.substring(9223372036854775807) == "x"`: "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 9223372036854775807 AND substringUTF8(`name`, 9223372036854775807) = 'x')",
		`name.substring(1, 3000000000) == "x"`:       "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 3000000000 AND substringUTF8(`name`, 2, 2999999999) = 'x')",
	} {
		got, err := trans.TranslateInline(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}

	got, err := trans.TranslateInline(filtertest.MustParseFilter(t, `name.substring(1, -9223372036854775808) == "x"`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Empty(t, got)
}

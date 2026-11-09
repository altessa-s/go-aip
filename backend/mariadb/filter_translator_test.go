// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/fieldpolicy"
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
		{"not equal", `status != "deleted"`, "(`status` != ? OR `status` IS NULL)", []any{"deleted"}},
		{"negated equal", `!(name == "John")`, "(`name` != ? OR `name` IS NULL)", []any{"John"}},
		{"field not equal field", `name != status`, "NOT (`name` <=> `status`)", nil},
		{"field equal field", `name == status`, "`name` <=> `status`", nil},
		{"greater than", `age > 18`, "`age` > ?", []any{int64(18)}},
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
			"NOT (`active` = TRUE)",
			nil,
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
			"LOCATE(?, `name`) > 0",
			[]any{"oh"},
		},
		{
			"contains keeps LIKE wildcards literal",
			`name.contains("100%_x")`,
			"LOCATE(?, `name`) > 0",
			[]any{"100%_x"},
		},
		{
			"startsWith",
			`name.startsWith("Jo")`,
			"LOCATE(?, `name`) = 1",
			[]any{"Jo"},
		},
		{
			"matches",
			`name.matches("^Jo.*n$")`,
			"`name` REGEXP ?",
			[]any{`^Jo.*n\z`},
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

// TestTranslator_EndsWithBindsNeedleThreeTimes pins the one predicate
// whose rendering consumes several bind slots for a single CEL operand —
// MariaDB has no endsWith(), so the needle guards the length, sizes the
// suffix and is then compared to it.
func TestTranslator_EndsWithBindsNeedleThreeTimes(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `name.endsWith("hn")`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "(CHAR_LENGTH(`name`) >= CHAR_LENGTH(?) AND RIGHT(`name`, CHAR_LENGTH(?)) = ?)", got)
	require.Equal(t, []any{"hn", "hn", "hn"}, args)
}

// TestTranslator_EndsWithArgumentOrder guards the alignment of the extra
// binds against their neighbors: a predicate that emits three
// placeholders must not shift the arguments of whatever follows it.
func TestTranslator_EndsWithArgumentOrder(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `age > 18 && name.endsWith("hn") && status == "x"`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t,
		"((`age` > ?) AND ((CHAR_LENGTH(`name`) >= CHAR_LENGTH(?) AND RIGHT(`name`, CHAR_LENGTH(?)) = ?))) AND (`status` = ?)", got)
	require.Equal(t, []any{int64(18), "hn", "hn", "hn", "x"}, args)
}

func TestTranslator_Size(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `tags.size() > 2`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "CHAR_LENGTH(`tags`) > ?", got)
	require.Equal(t, []any{int64(2)}, args)
}

func TestTranslator_Has(t *testing.T) {
	trans := mustTranslator(t, filter.WithAllowedFields("user.email"))
	node := filtertest.MustParseFilter(t, `has(user.email)`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "`user`.`email` IS NOT NULL", got)
	require.Empty(t, args)
}

func TestTranslator_QualifiedIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"two levels", `address.city == "NYC"`, "`address`.`city` = ?"},
	}

	trans := mustTranslator(t, filter.WithAllowedFields("address.city"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, _, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslator_RejectsLongAndDeepIdentifiers pins the identifier limits: a part the server would truncate reads another
// column, and a third part would qualify the column by a schema or database.
func TestTranslator_RejectsLongAndDeepIdentifiers(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 63) + "_shadow"
	trans, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("user.profile.name", long, "a."+long)))
	require.NoError(t, err)

	for _, expr := range []string{`user.profile.name == "John"`, long + ` == "x"`, `a.` + long + ` == "x"`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			got, args, err := trans.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
			require.Empty(t, args)

			got, err = trans.TranslateInline(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
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

func TestTranslator_FieldMappingCannotEscapeColumnPosition(t *testing.T) {
	tests := []struct {
		name   string
		mapped string
	}{
		{"backtick", "name` = '' OR 1 = 1 -- "},
		{"expression", "lower(name)"},
		{"empty", ""},
		{"empty segment", "address..city"},
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

func TestNewTranslator_UntrustedInputRequiresAllowlist(t *testing.T) {
	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	_, err = NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")))
	require.NoError(t, err)
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
		{"bool", `active == true`, "`active` = TRUE"},
		{"null", `deletedAt == null`, "`deletedAt` IS NULL"},
		{"in list", `status in ["a", "b"]`, "`status` IN ('a', 'b')"},
		{"contains", `name.contains("oh")`, "LOCATE('oh', `name`) > 0"},
		{"endsWith", `name.endsWith("hn")`, "(CHAR_LENGTH(`name`) >= CHAR_LENGTH('hn') AND RIGHT(`name`, CHAR_LENGTH('hn')) = 'hn')"},
		{"matches", `name.matches("^Jo")`, "`name` REGEXP '^Jo'"},
		{"matches end of text", `name.matches("^Jo$")`, "`name` REGEXP '^Jo\\\\z'"},
		{"matches multi-line", `name.matches("(?m)^Jo$")`, "`name` REGEXP '(?m)^Jo$'"},
		{
			"timestamp",
			`createdAt > timestamp("2024-01-02T03:04:05.123Z")`,
			"`createdAt` > '2024-01-02 03:04:05.123000'",
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

// TestQuoteString covers the escaping inline mode leans on, since it has
// no placeholder to hide behind. Doubling the quote is what closes the
// break-out; it holds in every sql_mode.
func TestQuoteString(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"single quote", `O'Brien`, `'O''Brien'`},
		{"backslash", `a\b`, `'a\\b'`},
		{"quote break-out attempt", `' OR 1=1 -- `, `''' OR 1=1 -- '`},
		{"escaped quote break-out attempt", `\' OR 1=1 -- `, `'\\'' OR 1=1 -- '`},
		{"newline", "a\nb", `'a\nb'`},
		{"nul", "a\x00b", `'a\0b'`},
		{"multi-byte rune", "Ünïcödé", `_utf8mb4 X'c39c6ec3af63c3b664c3a9'`},
		// U+4E3F ends in BF: in a gbk connection, a raw BF would pair with the backslash after it and end the literal.
		{"GBK breakout", "丿\\' OR 1=1 -- ", `_utf8mb4 X'e4b8bf5c27204f5220313d31202d2d20'`},
		{"invalid UTF-8", "\xff'", `_utf8mb4 X'ff27'`},
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
		{"false", false, "FALSE"},
		{"uint", uint64(7), "7"},
		{"negative int", int64(-7), "-7"},
		{"bytes", []byte{0xde, 0xad}, "X'dead'"},
		{
			"time is rendered in UTC",
			time.Date(2024, time.January, 2, 6, 4, 5, 0, time.FixedZone("MSK", 3*60*60)),
			"'2024-01-02 03:04:05.000000'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dialect{}.FormatLiteral(tt.value)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	// MariaDB's DOUBLE cannot hold the non-finite values and has no
	// literal for them, so they are rejected rather than approximated.
	for _, tt := range []struct {
		name  string
		value float64
	}{
		{"nan", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := dialect{}.FormatLiteral(tt.value)
			require.ErrorIs(t, err, filter.ErrUnsupportedType)
		})
	}

	t.Run("unsupported type", func(t *testing.T) {
		_, err := dialect{}.FormatLiteral(struct{}{})
		require.ErrorIs(t, err, filter.ErrUnsupportedType)
	})
}

// TestNewFilterTranslatorWithSchema_RejectsScalarOptions: the options only the scalar translator reads fail the schema
// translator instead of being ignored; its settings come from the schema.
func TestNewFilterTranslatorWithSchema_RejectsScalarOptions(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	schema, err := NewSchema(fields, WithSchemaContractMode(true))
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	for name, opt := range map[string]FilterOption{
		"literal encoder":        WithFilterLiteralEncoder(func(v any) (any, error) { return v, nil }),
		"predicate renderer":     WithFilterPredicateRenderer(func(filter.Node, sqlbase.ValueFunc) (string, bool, error) { return "", false, nil }),
		"decimal format":         WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 10, Scale: 2}),
		"default decimal format": WithFilterDecimalFormat(sqlbase.DefaultDecimalFormat()),
	} {
		_, err := NewFilterTranslatorWithSchema(policy, schema, opt)
		require.ErrorIs(t, err, filter.ErrInvalidOptions, name)
	}

	_, err = NewFilterTranslatorWithSchema(policy, schema, WithFilterCollector(nil), WithFilterOptions())
	require.NoError(t, err, "the shared options apply to both translators")
}

// TestTranslateInline_SubstringBoundsDoNotOverflow pins the bound arithmetic of substring(): the largest start is capped
// rather than wrapped to a negative position, and an end that wraps on subtraction is rejected.
func TestTranslateInline_SubstringBoundsDoNotOverflow(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t)

	for expr, want := range map[string]string{
		`name.substring(9223372036854775807) == "x"`: "(CHAR_LENGTH(`name`) >= 9223372036854775807 AND SUBSTRING(`name`, 9223372036854775807) = 'x')",
		`name.substring(1, 3000000000) == "x"`:       "(CHAR_LENGTH(`name`) >= 3000000000 AND SUBSTRING(`name`, 2, 2999999999) = 'x')",
	} {
		got, err := trans.TranslateInline(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}

	got, err := trans.TranslateInline(filtertest.MustParseFilter(t, `name.substring(1, -9223372036854775808) == "x"`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Empty(t, got)
}

// TestTranslator_RefusesUndeclaredDottedFields pins the qualifier rule: a dot names a table qualifier, so a dotted field
// the policy admits only through a wildcard or WithAnyField could reach a column of another joined table. A declared
// field (an allow-list entry, a type declaration or a field mapping) is rendered qualified.
func TestTranslator_RefusesUndeclaredDottedFields(t *testing.T) {
	t.Parallel()

	refusing := map[string]*filter.Policy{
		"any field":         filtertest.MustPolicy(t, filter.WithAnyField(true)),
		"wildcard access":   filtertest.MustPolicy(t, filter.WithFieldAccess(fieldpolicy.NewAllowList("a.*", "name"))),
		"declared sibling":  filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithAllowedFields("a.b", "name")),
		"undeclared prefix": filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"a": filter.FieldKindString})),
	}

	for name, policy := range refusing {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			trans, err := NewFilterTranslator(policy)
			require.NoError(t, err)

			got, args, err := trans.Translate(filtertest.MustParseFilter(t, `a.secret == "x"`))
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
			require.Empty(t, got)
			require.Empty(t, args)

			got, err = trans.TranslateInline(filtertest.MustParseFilter(t, `name == "x" || a.secret == "x"`))
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
			require.Empty(t, got)
		})
	}

	declaring := map[string]*filter.Policy{
		"allow-list entry": filtertest.MustPolicy(t, filter.WithAllowedFields("a.secret")),
		"type declaration": filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"a.secret": filter.FieldKindString})),
	}

	for name, policy := range declaring {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			trans, err := NewFilterTranslator(policy)
			require.NoError(t, err)

			_, _, err = trans.Translate(filtertest.MustParseFilter(t, `a.secret == "x"`))
			require.NoError(t, err)
		})
	}

	mapped, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)),
		WithFilterOptions(filter.WithFieldMapping(map[string]string{"a.secret": "t.secret"})))
	require.NoError(t, err)

	_, _, err = mapped.Translate(filtertest.MustParseFilter(t, `a.secret == "x"`))
	require.NoError(t, err, "a mapped field is declared")
}

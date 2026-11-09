// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

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
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{"equal string", `name == "John"`, `"name" = $1`, []any{"John"}},
		{"equal int", `age == 25`, `"age" = $1`, []any{int64(25)}},
		{"equal bool", `active == true`, `"active" = $1`, []any{true}},
		{"not equal", `status != "deleted"`, `("status" != $1 OR "status" IS NULL)`, []any{"deleted"}},
		{"greater than", `age > 18`, `"age" > $1`, []any{int64(18)}},
		{"less or equal", `age <= 65`, `"age" <= $1`, []any{int64(65)}},
		{"reversed operands", `18 < age`, `$1 < "age"`, []any{int64(18)}},
		{"negated equal", `!(name == "John")`, `("name" != $1 OR "name" IS NULL)`, []any{"John"}},
		{"field not equal field", `name != status`, `"name" IS DISTINCT FROM "status"`, nil},
		{"negated field equal field", `!(name == status)`, `"name" IS DISTINCT FROM "status"`, nil},
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
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"equal null", `deletedAt == null`, `"deletedAt" IS NULL`},
		{"not equal null", `deletedAt != null`, `"deletedAt" IS NOT NULL`},
		{"null on the left", `null == deletedAt`, `"deletedAt" IS NULL`},
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

// TestTranslator_PlaceholdersAreNumberedInOrder is the invariant unique
// to PostgreSQL: a positional dialect can repeat "?", but $n must count
// up in exactly the order the arguments are collected.
func TestTranslator_PlaceholdersAreNumberedInOrder(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t,
		`a == 1 && b in [2, 3] && c.contains("x") && d == 4`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t,
		`(("a" = $1) AND ("b" IN ($2, $3))) AND ((strpos("c", $4) > 0) AND ("d" = $5))`,
		got)
	require.Equal(t, []any{int64(1), int64(2), int64(3), "x", int64(4)}, args)
}

// TestTranslator_NumberingRestartsPerCall guards the state reset: a
// second Translate must begin again at $1, not continue the first
// call's sequence.
func TestTranslator_NumberingRestartsPerCall(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)

	first := filtertest.MustParseFilter(t, `a == 1 && b == 2`)
	got, args, err := trans.Translate(first)
	require.NoError(t, err)
	require.Equal(t, `("a" = $1) AND ("b" = $2)`, got)
	require.Equal(t, []any{int64(1), int64(2)}, args)

	second := filtertest.MustParseFilter(t, `c == 3`)
	got, args, err = trans.Translate(second)
	require.NoError(t, err)
	require.Equal(t, `"c" = $1`, got)
	require.Equal(t, []any{int64(3)}, args)
}

// TestTranslator_EmptyInDoesNotConsumeANumber pins the rollback in the
// empty-IN path: discarded placeholders must not leave a gap in the
// numbering of the clauses around them.
func TestTranslator_EmptyInDoesNotConsumeANumber(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `status in [] || name == "John"`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `(`+sqlbase.MatchNone+`) OR ("name" = $1)`, got)
	require.Equal(t, []any{"John"}, args)
}

func TestTranslator_LogicalOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"and",
			`name == "John" && age >= 18`,
			`("name" = $1) AND ("age" >= $2)`,
			[]any{"John", int64(18)},
		},
		{
			"or",
			`status == "active" || status == "pending"`,
			`("status" = $1) OR ("status" = $2)`,
			[]any{"active", "pending"},
		},
		{
			"not on bare ident",
			`!active`,
			`NOT ("active" = TRUE)`,
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
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
		args []any
	}{
		{
			"contains",
			`name.contains("oh")`,
			`strpos("name", $1) > 0`,
			[]any{"oh"},
		},
		{
			"contains keeps LIKE wildcards literal",
			`name.contains("100%_x")`,
			`strpos("name", $1) > 0`,
			[]any{"100%_x"},
		},
		{
			"startsWith",
			`name.startsWith("Jo")`,
			`starts_with("name", $1)`,
			[]any{"Jo"},
		},
		{
			"matches",
			`name.matches("^Jo.*n$")`,
			`"name" ~ $1`,
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

// TestTranslator_EndsWithBindsNeedleTwice pins the one predicate whose
// rendering consumes two bind slots for a single CEL operand — PostgreSQL
// has no endsWith(), so the needle sizes the suffix and is then compared
// to it. The two placeholders must be numbered consecutively.
func TestTranslator_EndsWithBindsNeedleTwice(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `age > 18 && name.endsWith("hn") && status == "x"`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t,
		`(("age" > $1) AND (right("name", length($2)) = $3)) AND ("status" = $4)`,
		got)
	require.Equal(t, []any{int64(18), "hn", "hn", "x"}, args)
}

func TestTranslator_Size(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `tags.size() > 2`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `length("tags") > $1`, got)
	require.Equal(t, []any{int64(2)}, args)
}

func TestTranslator_Has(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t, filter.WithAllowedFields("user.email"))
	node := filtertest.MustParseFilter(t, `has(user.email)`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `"user"."email" IS NOT NULL`, got)
	require.Empty(t, args)
}

func TestTranslator_QualifiedIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"two levels", `address.city == "NYC"`, `"address"."city" = $1`},
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

// TestTranslator_QuotingPreservesCase pins the reason identifiers are
// always quoted: PostgreSQL folds an unquoted name to lower case, which
// would silently target a different column.
func TestTranslator_QuotingPreservesCase(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `createdAt > 1`)

	got, _, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `"createdAt" > $1`, got)
}

func TestTranslator_FieldMapping(t *testing.T) {
	t.Parallel()
	trans := mustTranslator(t, filter.WithFieldMapping(map[string]string{
		"organizationIds": "organization_id",
		"createdAt":       "created_at",
	}))
	node := filtertest.MustParseFilter(t, `organizationIds in ["org-1"] && createdAt > 100`)

	got, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `("organization_id" IN ($1)) AND ("created_at" > $2)`, got)
	require.Equal(t, []any{"org-1", int64(100)}, args)
}

func TestTranslator_FieldMappingCannotEscapeColumnPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mapped string
	}{
		{"double quote", `name" = '' OR 1 = 1 -- `},
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
	t.Parallel()

	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	_, err = NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")))
	require.NoError(t, err)
}

func TestTranslateInline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"string", `name == "John"`, `"name" = E'John'`},
		{"int", `age == 25`, `"age" = 25`},
		{"float", `price >= 10.5`, `"price" >= 10.5`},
		{"bool", `active == true`, `"active" = TRUE`},
		{"null", `deletedAt == null`, `"deletedAt" IS NULL`},
		{"in list", `status in ["a", "b"]`, `"status" IN (E'a', E'b')`},
		{"contains", `name.contains("oh")`, `strpos("name", E'oh') > 0`},
		{"endsWith", `name.endsWith("hn")`, `right("name", length(E'hn')) = E'hn'`},
		{"matches", `name.matches("^Jo")`, `"name" ~ E'^Jo'`},
		{
			"timestamp",
			`createdAt > timestamp("2024-01-02T03:04:05.123Z")`,
			`"createdAt" > TIMESTAMP WITH TIME ZONE '2024-01-02 03:04:05.123000+00'`,
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
// no placeholder to hide behind. The E prefix is what makes one rule
// correct regardless of standard_conforming_strings.
func TestQuoteString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"single quote", `O'Brien`, `E'O''Brien'`},
		{"backslash", `a\b`, `E'a\\b'`},
		{"quote break-out attempt", `' OR 1=1 -- `, `E''' OR 1=1 -- '`},
		{"escaped quote break-out attempt", `\' OR 1=1 -- `, `E'\\'' OR 1=1 -- '`},
		{"newline", "a\nb", `E'a\nb'`},
		{"nul", "a\x00b", `E'a\000b'`},
		{"multi-byte rune", "Ünïcödé", `E'\u00dcn\u00efc\u00f6d\u00e9'`},
		{"astral rune", "a😀b", `E'a\U0001f600b'`},
		{"invalid UTF-8", "a\xffb", `E'a\xffb'`},
		// U+4E3F ends in BF and U+4E01 in 81: in GBK or SJIS the raw byte would pair with the next backslash.
		{"GBK breakout", "丿\\' OR 1=1 -- ", `E'\u4e3f\\'' OR 1=1 -- '`},
		{"SJIS breakout", "丁\\' OR 1=1 -- ", `E'\u4e01\\'' OR 1=1 -- '`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteString(tt.value))
		})
	}
}

func TestFormatLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "NULL"},
		{"false", false, "FALSE"},
		{"uint", uint64(7), "7"},
		{"negative int", int64(-7), "-7"},
		{"nan", math.NaN(), "'NaN'::double precision"},
		{"positive infinity", math.Inf(1), "'Infinity'::double precision"},
		{"negative infinity", math.Inf(-1), "'-Infinity'::double precision"},
		{"bytes", []byte{0xde, 0xad}, "decode('dead', 'hex')"},
		{
			"time is rendered in UTC",
			time.Date(2024, time.January, 2, 6, 4, 5, 0, time.FixedZone("MSK", 3*60*60)),
			"TIMESTAMP WITH TIME ZONE '2024-01-02 03:04:05.000000+00'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterDialect{}.FormatLiteral(tt.value)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	t.Run("unsupported type", func(t *testing.T) {
		_, err := filterDialect{}.FormatLiteral(struct{}{})
		require.ErrorIs(t, err, filter.ErrUnsupportedType)
	})
}

// TestNewFilterTranslatorWithSchema_RejectsScalarOptions: the options only the scalar translator reads fail the schema
// translator instead of being ignored.
func TestNewFilterTranslatorWithSchema_RejectsScalarOptions(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	schema, err := NewSchema(fields)
	require.NoError(t, err)

	for name, opt := range map[string]FilterOption{
		"literal encoder":    WithFilterLiteralEncoder(func(v any) (any, error) { return v, nil }),
		"predicate renderer": WithFilterPredicateRenderer(func(filter.Node, sqlbase.ValueFunc) (string, bool, error) { return "", false, nil }),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema, opt)
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}
}

// TestTranslateInline_SubstringBoundsDoNotOverflow pins the bound arithmetic of substring(): the largest start is capped
// rather than wrapped to a negative position, and an end that wraps on subtraction is rejected.
func TestTranslateInline_SubstringBoundsDoNotOverflow(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t)

	for expr, want := range map[string]string{
		`name.substring(9223372036854775807) == "x"`: `(length("name") >= 9223372036854775807 AND substr("name", 2147483647) = E'x')`,
		`name.substring(1, 3000000000) == "x"`:       `(length("name") >= 3000000000 AND substr("name", 2, 2147483647) = E'x')`,
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

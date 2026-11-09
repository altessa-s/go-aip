// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// The walker itself is covered end-to-end by the clickhouse, mariadb and
// postgres suites, which exercise it through three real dialects. What
// those cannot reach is the walker's behavior when a dialect fails: every
// production dialect succeeds on the inputs the parser can produce. The
// fake below fails on demand so the propagation paths are pinned.

// errDialect is a sentinel returned by failing.
var errDialect = errors.New("dialect failure")

// failing is a dialect whose selected method always fails. The zero
// value succeeds everywhere, so each test opts into exactly one failure.
type failing struct {
	quoteIdent    bool
	formatLiteral bool
	stringPred    bool
}

func (d failing) QuoteIdent(name string) (string, error) {
	if d.quoteIdent {
		return "", errDialect
	}

	return sqlbase.QuoteQualified(name, '"', sqlbase.PostgresMaxIdentLen)
}

func (failing) Placeholder(int) string { return "?" }

func (failing) SizeExpr(col string) string { return "length(" + col + ")" }

func (d failing) FormatLiteral(v any) (string, error) {
	if d.formatLiteral {
		return "", errDialect
	}

	if v == nil {
		return "NULL", nil
	}

	return "<literal>", nil
}

func (d failing) StringPredicate(_ filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	if d.stringPred {
		return "", errDialect
	}

	arg, err := value(needle)
	if err != nil {
		return "", err
	}

	return col + " ~ " + arg, nil
}

func mustTranslator(tb testing.TB, d sqlbase.FilterDialect, opts ...filter.PolicyOption) *sqlbase.FilterTranslator {
	tb.Helper()

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(tb, anyField(opts)...), d)
	require.NoError(tb, err)

	return tr
}

// anyField prepends filter.WithAnyField to opts: these tests pin translation itself, not the allow-list, so a test without
// WithAllowedFields translates any field; an allow-list given in opts still applies.
func anyField(opts []filter.PolicyOption) []filter.PolicyOption {
	return append([]filter.PolicyOption{filter.WithAnyField(true)}, opts...)
}

func TestTranslate_PropagatesDialectErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect failing
		expr    string
	}{
		{"QuoteIdent", failing{quoteIdent: true}, `name == "x"`},
		{"StringPredicate", failing{stringPred: true}, `name.contains("x")`},
		// FormatLiteral is reached in the parameterized mode only through
		// the boolean test a bare identifier compiles to.
		{"FormatLiteral via bare identifier", failing{formatLiteral: true}, `active && verified`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trans := mustTranslator(t, tt.dialect)
			node := filtertest.MustParseFilter(t, tt.expr)

			_, _, err := trans.Translate(node)
			require.ErrorIs(t, err, errDialect)
		})
	}
}

func TestTranslateInline_PropagatesFormatLiteralError(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, failing{formatLiteral: true})
	node := filtertest.MustParseFilter(t, `name == "x"`)

	_, err := trans.TranslateInline(node)
	require.ErrorIs(t, err, errDialect)
}

// TestTranslate_ModeIsNotSticky guards the per-call reset of the inline
// flag: a TranslateInline must not leave the walker rendering literals
// in place for the Translate that follows it.
func TestTranslate_ModeIsNotSticky(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, failing{})
	node := filtertest.MustParseFilter(t, `name == "x"`)

	inline, err := trans.TranslateInline(node)
	require.NoError(t, err)
	require.Equal(t, `"name" = <literal>`, inline)

	where, args, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `"name" = ?`, where)
	require.Equal(t, []any{"x"}, args)
}

// TestTranslateInline_CollectsNoArguments pins that inline mode leaves
// the argument slice untouched — a caller that mixed the two modes would
// otherwise bind values the SQL text never references.
func TestTranslateInline_CollectsNoArguments(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, failing{})
	node := filtertest.MustParseFilter(t, `a == 1 && b == 2`)

	_, err := trans.TranslateInline(node)
	require.NoError(t, err)

	_, args, err := trans.Translate(filtertest.MustParseFilter(t, `c == 3`))
	require.NoError(t, err)
	require.Equal(t, []any{int64(3)}, args)
}

func TestTranslate_RejectsUnsupportedLiteralType(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, failing{})
	// Hand-built: the parser only emits literal types the drivers bind,
	// so the guard is unreachable through Parse. It exists for custom
	// functions, which may put an arbitrary value into a LiteralNode.
	node := &filter.BinaryOpNode{
		Op:    filter.OpEqual,
		Left:  &filter.IdentNode{Name: "name"},
		Right: &filter.LiteralNode{Value: struct{}{}},
	}

	_, _, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
}

func TestNew_RequiresPolicy(t *testing.T) {
	t.Parallel()

	_, err := sqlbase.NewFilterTranslator(nil, failing{})
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
	_, err = sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), nil)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

// TestNew_RejectsDecimal: a FieldKindDecimal declaration fails construction instead of comparing through a float.
func TestNew_RejectsDecimal(t *testing.T) {
	t.Parallel()

	for name, opt := range map[string]filter.PolicyOption{
		"field types": filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, opt, filter.WithAnyField(true)), failing{})
			require.ErrorIs(t, err, filter.ErrUnsupportedType)
		})
	}

	_, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindFloat}), filter.WithAnyField(true)), failing{})
	require.NoError(t, err, "other kinds are accepted")
}

// TestListAsPredicate walks a list literal through Translate, the only way to reach the walker's list visit now that the
// visit methods are unexported: a list is a value, never a clause, and an element the walker cannot bind fails first.
func TestListAsPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *filter.ListNode
		want error
	}{
		{
			name: "values are not a clause",
			node: &filter.ListNode{Elements: []filter.Node{
				&filter.LiteralNode{Value: int64(1)}, &filter.LiteralNode{Value: "two"}, &filter.LiteralNode{Value: nil},
			}},
			want: filter.ErrInvalidExpression,
		},
		{name: "empty list", node: &filter.ListNode{}, want: filter.ErrInvalidExpression},
		{
			name: "element failure propagates",
			node: &filter.ListNode{Elements: []filter.Node{&filter.LiteralNode{Value: struct{}{}}}},
			want: filter.ErrUnsupportedType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := mustTranslator(t, failing{}).Translate(tc.node)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestVisitorMethodsUnexported pins the decision that the walker's filter.Visitor methods are not part of the public
// surface: calling them on a shared translator would race with concurrent translations and skip the checks of a run.
func TestVisitorMethodsUnexported(t *testing.T) {
	t.Parallel()

	var trans any = mustTranslator(t, failing{})

	_, ok := trans.(filter.Visitor)
	require.False(t, ok)
}

type acceptingMatcher struct{}

func (acceptingMatcher) MatchString(string) bool { return true }

func TestCustomRegexSyntax(t *testing.T) {
	t.Parallel()
	tr := mustTranslator(t, failing{},
		filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { return acceptingMatcher{}, nil }),
		filter.WithRegexValidator(func(string) error { return nil }))
	_, args, err := tr.Translate(filtertest.MustParseFilter(t, `name.matches("(?<=a)b")`))
	require.NoError(t, err)
	require.Equal(t, []any{"(?<=a)b"}, args)
}

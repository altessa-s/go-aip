// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// mustTranslator builds a translator and fails the test on any
// construction error. Keeps the success-path tests free of
// error-wiring noise; tests that exercise construction failures call
// [NewFilterTranslator] directly.
func mustTranslator(tb testing.TB, tableVar string, opts ...any) *FilterTranslator {
	tb.Helper()

	policy, binding := anyField(tb, opts)
	tr, err := NewFilterTranslator(policy, WithFilterTableVar(tableVar), binding)
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
	}{
		{
			name: "equal int",
			expr: `field == 42`,
			want: `(d["field"] == 42)`,
		},
		{
			name: "equal string",
			expr: `field == "val"`,
			want: `(d["field"] == "val")`,
		},
		{
			name: "equal bool true",
			expr: `field == true`,
			want: `(d["field"] == true)`,
		},
		{
			name: "equal bool false",
			expr: `field == false`,
			want: `(d["field"] == false)`,
		},
		{
			name: "equal null",
			expr: `field == null`,
			want: `(d["field"] == nil)`,
		},
		{
			name: "not equal",
			expr: `field != 0`,
			want: `(d["field"] == nil or (type(d["field"]) == "number" and d["field"] ~= 0))`,
		},
		{
			name: "greater than",
			expr: `field > 10`,
			want: `(type(d["field"]) == "number" and d["field"] > 10)`,
		},
		{
			name: "greater than or equal",
			expr: `field >= 10`,
			want: `(type(d["field"]) == "number" and d["field"] >= 10)`,
		},
		{
			name: "less than",
			expr: `field < 10`,
			want: `(type(d["field"]) == "number" and d["field"] < 10)`,
		},
		{
			name: "less than or equal",
			expr: `field <= 10`,
			want: `(type(d["field"]) == "number" and d["field"] <= 10)`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_LogicalOperators(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "and",
			expr: `a == 1 && b == 2`,
			want: `((d["a"] == 1) and (d["b"] == 2))`,
		},
		{
			name: "or",
			expr: `a == 1 || b == 2`,
			want: `((d["a"] == 1) or (d["b"] == 2))`,
		},
		{
			name: "not ident",
			expr: `!active`,
			want: `(d["active"] == false)`,
		},
		{
			name: "not complex",
			expr: `!(status == 1)`,
			want: `(d["status"] == nil or (type(d["status"]) == "number" and d["status"] ~= 1))`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_NestedFields(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "two levels",
			expr: `addr.city == "NYC"`,
			want: `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "addr", "city") == "NYC")`,
		},
		{
			name: "three levels",
			expr: `a.b.c == 1`,
			want: `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "a", "b", "c") == 1)`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_InOperator(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "string list",
			expr: `field in ["a", "b"]`,
			want: `(d["field"] == "a" or d["field"] == "b")`,
		},
		{
			name: "single value",
			expr: `field in ["only"]`,
			want: `(d["field"] == "only")`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_StringFunctions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "contains",
			expr: `field.contains("sub")`,
			want: `(type(d["field"]) == "string" and string.find(d["field"], "sub", 1, true) ~= nil)`,
		},
		{
			name: "startsWith",
			expr: `field.startsWith("pre")`,
			want: `(type(d["field"]) == "string" and string.sub(d["field"], 1, 3) == "pre")`,
		},
		{
			name: "endsWith",
			expr: `field.endsWith("suf")`,
			want: `(type(d["field"]) == "string" and string.sub(d["field"], -3) == "suf")`,
		},
		{
			// string.sub(s, -0) is the whole string, so an empty suffix
			// gets a form that is true for every string.
			name: "endsWith empty",
			expr: `field.endsWith("")`,
			want: `(type(d["field"]) == "string" and string.sub(d["field"], 1, 0) == "")`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_UnsupportedOperations(t *testing.T) {
	trans := mustTranslator(t, "")

	node := filtertest.MustParseFilter(t, `name.matches("^A.*")`)
	_, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestTranslator_HasFunction(t *testing.T) {
	trans := mustTranslator(t, "")

	node := filtertest.MustParseFilter(t, `has(user.email)`)
	got, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "user", "email") ~= nil)`, got)
}

func TestTranslator_SizeFunction(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "size equal",
			expr: `tags.size() == 3`,
			want: `((type(d["tags"]) == "string" or type(d["tags"]) == "table") and (type(d["tags"]) == "string" and select(2, string.gsub(d["tags"], "[%z\1-\127\194-\244]", "")) or #d["tags"]) == 3)`,
		},
		{
			name: "size greater than",
			expr: `tags.size() > 0`,
			want: `((type(d["tags"]) == "string" or type(d["tags"]) == "table") and (type(d["tags"]) == "string" and select(2, string.gsub(d["tags"], "[%z\1-\127\194-\244]", "")) or #d["tags"]) > 0)`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_NilNode(t *testing.T) {
	trans := mustTranslator(t, "")

	got, err := trans.Translate(nil)
	require.NoError(t, err)
	require.Equal(t, "true", got)
}

func TestTranslator_WithAllowedFields(t *testing.T) {
	trans := mustTranslator(t, "", filter.WithAllowedFields("name", "age"))

	t.Run("allowed field", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name == "John"`)
		_, err := trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("disallowed field", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `email == "test@example.com"`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	})
}

func TestTranslator_WithFieldMapping(t *testing.T) {
	trans := mustTranslator(t, "",
		filter.WithFieldMapping(map[string]string{
			"userName": "user_name",
		}),
	)

	node := filtertest.MustParseFilter(t, `userName == "John"`)
	got, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `(d["user_name"] == "John")`, got)
}

func TestTranslator_WithMaxDepth(t *testing.T) {
	trans := mustTranslator(t, "", filter.WithMaxDepth(2))

	t.Run("within depth", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `a == 1 && b == 2`)
		_, err := trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("exceeds depth", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `(a == 1 && b == 2) && (c == 3 && d == 4)`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
	})
}

func TestTranslator_CustomTableVar(t *testing.T) {
	trans := mustTranslator(t, "item")

	node := filtertest.MustParseFilter(t, `name == "test"`)
	got, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `(item["name"] == "test")`, got)
}

func TestTranslator_StringEscaping(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "double quotes",
			expr: `field == "say \"hello\""`,
			want: `(d["field"] == "say \"hello\"")`,
		},
		{
			name: "backslash",
			expr: `field == "path\\to"`,
			want: `(d["field"] == "path\\to")`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_ComplexExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "and with in",
			expr: `status == 1 && (role in ["admin", "editor"])`,
			want: `((d["status"] == 1) and (d["role"] == "admin" or d["role"] == "editor"))`,
		},
		{
			name: "triple and",
			expr: `a == 1 && b == 2 && c == 3`,
			want: `(((d["a"] == 1) and (d["b"] == 2)) and (d["c"] == 3))`,
		},
	}

	trans := mustTranslator(t, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslator_VisitMethodsUnexported pins that a FilterTranslator exposes no filter.Visitor methods: they mutate the per-call
// traversal state, so calling them on a shared translator would race with concurrent translations and skip the checks of Translate.
func TestTranslator_VisitMethodsUnexported(t *testing.T) {
	t.Parallel()

	_, isVisitor := any(mustTranslator(t, "")).(filter.Visitor)
	require.False(t, isVisitor)
}

// TestNewTranslator_DefaultTableVar pins the positional-argument
// default: an empty tableVar resolves to "d". The MaxDepth default is
// covered at the canonical level by
// aip/filter::TestTranslatorContext_MaxDepth and is not re-asserted
// here.
func TestNewTranslator_DefaultTableVar(t *testing.T) {
	trans := mustTranslator(t, "")
	require.Equal(t, "d", trans.tableVar)
}

// TestNewTranslator_UntrustedInput_RequiresAllowlist locks the contract
// that misconfiguration is rejected at construction, not at the first
// Translate call.
func TestNewTranslator_UntrustedInput_RequiresAllowlist(t *testing.T) {
	t.Parallel()

	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	_, err = NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")), WithFilterTableVar("row"))
	require.NoError(t, err)
}

// TestNewTranslator_UntrustedInput_EmptyAllowlist guards the
// misconfiguration path where WithAllowedFields was called with no
// arguments (or with an empty slice from a config loader). The check
// has to happen at construction so the bug surfaces during boot, not
// on the first untrusted query.
func TestNewTranslator_UntrustedInput_EmptyAllowlist(t *testing.T) {
	t.Parallel()

	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAllowedFields())
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	var empty []string

	_, err = filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAllowedFields(empty...))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
}

// Nothing is a member of an empty list; the translation must be valid Lua
// rather than the empty group `()`.
func TestTranslator_InEmptyList(t *testing.T) {
	t.Parallel()
	tr := mustTranslator(t, "d")

	got, err := tr.Translate(filtertest.MustParseFilter(t, `status in []`))
	require.NoError(t, err)
	require.Equal(t, luaFalse, got)

	got, err = tr.Translate(filtertest.MustParseFilter(t, `!(status in [])`))
	require.NoError(t, err)
	require.Equal(t, luaTrue, got)
}

// TestNewTranslator_RejectsDecimal: a FieldKindDecimal declaration fails construction instead of comparing through a float.
func TestNewTranslator_RejectsDecimal(t *testing.T) {
	t.Parallel()

	_, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}), filter.WithAnyField(true)), WithFilterTableVar("row"))
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
}

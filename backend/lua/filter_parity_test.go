// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"

	glua "github.com/yuin/gopher-lua"
)

// toLua converts a record value to its Lua form: a map or a list becomes a table.
func toLua(l *glua.LState, v any) glua.LValue {
	switch x := v.(type) {
	case nil:
		return glua.LNil
	case bool:
		return glua.LBool(x)
	case int:
		return glua.LNumber(x)
	case int64:
		return glua.LNumber(x)
	case float64:
		return glua.LNumber(x)
	case string:
		return glua.LString(x)
	case []any:
		t := l.NewTable()
		for _, el := range x {
			t.Append(toLua(l, el))
		}

		return t
	case map[string]any:
		t := l.NewTable()
		for k, el := range x {
			t.RawSetString(k, toLua(l, el))
		}

		return t
	default:
		panic(fmt.Sprintf("unsupported record value %T", v))
	}
}

// runLua evaluates expr against record bound to d and returns the Lua result.
func runLua(t *testing.T, expr string, record map[string]any) glua.LValue {
	t.Helper()

	l := glua.NewState()
	t.Cleanup(l.Close)
	l.SetGlobal("d", toLua(l, record))
	require.NoError(t, l.DoString("result = "+expr), "expr=%s", expr)

	return l.GetGlobal("result")
}

// TestLuaMatchesEvaluator runs each expression through the Lua translator and gopher-lua and through the evaluator, the
// reference semantics, on the same records.
func TestLuaMatchesEvaluator(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags", "scores"))
	tr, err := lua.NewFilterTranslator(policy)
	require.NoError(t, err)
	ev, err := evaluator.New(policy)
	require.NoError(t, err)

	records := []map[string]any{
		{"tags": []any{"red", "blue"}, "scores": []any{int64(1), int64(5)}, "active": true, "name": "hello", "other": "ell", "n": int64(3)},
		{"tags": []any{"green"}, "scores": []any{}, "active": "no", "name": "héllo", "other": "x"},
		{"tags": "red", "active": false, "name": int64(7), "addr": map[string]any{"city": "Oslo"}, "other": true},
		{"addr": "flat", "n": "3", "other": map[string]any{}},
		{"addr": int64(5)},
		{"addr": true, "n": false},
		{"addr": map[string]any{"city": "Rome"}, "other": map[string]any{"city": "Rome"}, "n": 3.0, "name": "x"},
		{},
	}

	exprs := []string{
		// F3: array fields are tested element by element.
		`tags == "red"`, `tags != "red"`, `"red" in tags`, `tags in ["blue", "green"]`, `tags == null`, `tags != null`,
		`scores > 3`, `scores <= 1`, `scores < "a"`, `tags.size() == 2`, `scores.size() == 0`,
		`tags.startsWith("gr")`, `tags.contains("lu")`, `tags.endsWith("ed")`, `!(tags == "red")`,
		// F4: a bare boolean is exactly true, the complement of its negation.
		`active`, `!active`, `active || !active`,
		// F5: string functions with field arguments; non-string targets are false.
		`name.contains(other)`, `name.startsWith("he")`, `name.endsWith("llo")`, `name.endsWith("")`, `name.contains("é")`,
		// F10: substring as a comparison operand.
		`name.substring(1, 3) == "el"`, `name.substring(1, 3) == "él"`, `name.substring(2) == "llo"`, `"el" == name.substring(1, 3)`,
		`name.substring(0, 1) < "i"`, `name.substring(n) == "lo"`,
		// D5: an ordered comparison of a null, absent, boolean, table or differently typed value does not match instead of
		// raising a Lua error; a nested field under an absent or non-table parent is nil.
		`n > 2`, `n <= 3`, `name < "i"`, `name < other`, `n < other`, `active < 3`, `other > "a"`, `name.size() > n`,
		`tags.size() >= n`, `scores > n`, `addr.city == "Oslo"`, `addr.city < "P"`, `has(addr.city)`, `addr.city != null`,
		// Three-valued logic: what the evaluator finds unknown (or fails) does not match, also under negation, and && and ||
		// absorb it in either order.
		`!(n > 2)`, `!(name < other)`, `!(n < other)`, `!(n > 2 || name == "x")`, `name == "x" || n > 2`, `n > 2 || has(addr.city)`,
		`!(n > 2 && active)`, `!(active && n > 2)`, `!(!(n > 2))`, `!(name.size() > n)`, `!(name.size() == 5)`, `!(size(addr) == 0)`,
		`!name.contains(other)`, `!name.startsWith(other)`, `!tags.contains(other)`, `!tags.contains("e")`, `!(scores > n)`,
		`!(scores > 3)`, `!(tags.size() >= n)`, `!(name.substring(n) == "lo")`, `!(name.substring(1, 3) < "f")`,
		`!("el" == name.substring(1, 3))`, `!has(addr.city)`, `!(n in [3, 4])`, `!("red" in tags)`, `!(tags < "c")`,
		`!(tags != "red")`, `!(tags in ["blue"])`, `!(addr.city != null)`, `!(active || n > 2)`,
		// Equality with a value of another type is unknown in the evaluator, so neither == nor != (nor a negated ==) matches
		// it; null equality is two-valued.
		`n == 3`, `n != 3`, `!(n == 3)`, `!(n != 3)`, `name != "hello"`, `!(name == "hello")`, `active != true`,
		`!(active == false)`, `addr.city != "Oslo"`, `!(addr.city == "Oslo")`, `other != "x"`, `n != 3.0`, `n != null`,
		`!(n == null)`, `addr.city == null`, `!(addr.city == null)`, `n == other`, `n != other`, `!(n == other)`,
		`!(n != other)`, `name != other`, `!(name == other)`, `addr != "flat"`, `!(addr == 5)`, `addr != other`,
		`!(addr == other)`, `addr == other`,
	}

	for _, expr := range exprs {
		node := filtertest.MustParseFilter(t, expr)
		q, err := tr.Translate(node)
		require.NoError(t, err, expr)

		for i, rec := range records {
			want, evalErr := ev.Evaluate(node, rec)
			if evalErr != nil {
				// The evaluator fails a record it cannot evaluate; the Lua expression must still run without an error (one
				// would abort the whole script) and not match.
				assert.Equal(t, glua.LFalse, runLua(t, q, rec), "expr=%s record=%d query=%s evalErr=%v", expr, i, q, evalErr)

				continue
			}

			assert.Equal(t, glua.LBool(want), runLua(t, q, rec), "expr=%s record=%d query=%s", expr, i, q)
		}
	}
}

// TestLuaNestedFieldUnderNonTable reads nested fields under parents that are not tables. Indexing a number or a boolean
// raises a Lua error that would abort the whole EVAL script, and indexing a string would read the string library; every read
// must instead be nil, so the predicate does not match, as in the evaluator.
func TestLuaNestedFieldUnderNonTable(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	tr, err := lua.NewFilterTranslator(policy)
	require.NoError(t, err)
	ev, err := evaluator.New(policy)
	require.NoError(t, err)

	records := map[string]map[string]any{
		"number parent":         {"a": int64(5)},
		"float parent":          {"a": 1.5},
		"boolean parent":        {"a": true},
		"false parent":          {"a": false},
		"string parent":         {"a": "len"},
		"null parent":           {"a": nil},
		"absent parent":         {},
		"number grandparent":    {"a": map[string]any{"b": int64(7)}},
		"string grandparent":    {"a": map[string]any{"b": "upper"}},
		"list parent":           {"a": []any{int64(1)}},
		"table parent":          {"a": map[string]any{"b": map[string]any{"c": int64(1)}, "len": int64(1)}},
		"table parent no field": {"a": map[string]any{}},
	}
	exprs := []string{
		`a.b == 1`, `a.len == 1`, `has(a.b)`, `!has(a.b)`, `a.b == null`, `a.b != null`, `a.b > 0`,
		`a.b.c == 1`, `has(a.b.c)`, `a.b.c > 0`, `!(a.b.c == 1)`, `a.b.size() == 1`, `a.b.contains("x")`,
		`a.b.c.startsWith("u")`, `a.len.endsWith(a.b)`, `a.b in [1, 2]`,
	}

	for name, rec := range records {
		for _, expr := range exprs {
			node := filtertest.MustParseFilter(t, expr)
			q, err := tr.Translate(node)
			require.NoError(t, err, expr)

			want, evalErr := ev.Evaluate(node, rec)
			if evalErr != nil {
				want = false
			}

			assert.Equal(t, glua.LBool(want), runLua(t, q, rec), "record=%s expr=%s query=%s", name, expr, q)
		}
	}
}

func TestLuaBareBooleanIsExact(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `active`))
	require.NoError(t, err)
	assert.Equal(t, `(d["active"] == true)`, q)
	assert.Equal(t, glua.LFalse, runLua(t, q, map[string]any{"active": "no"}))
}

func TestLuaErrors(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want error
	}{
		{`name.contains(1)`, filter.ErrInvalidExpression},
		{`name.contains(other.size())`, filter.ErrUnsupportedOperation},
		{`name.substring(2, 1) == "x"`, filter.ErrInvalidExpression},
		{`name.substring(-1) == "x"`, filter.ErrInvalidExpression},
		{`name.substring("a") == "x"`, filter.ErrInvalidExpression},
		{`name.substring(0, 1).startsWith("x")`, filter.ErrUnsupportedOperation},
		{`name.matches("x")`, filter.ErrUnsupportedOperation},
		{`name.contains("a", "b")`, filter.ErrInvalidExpression},
		{`name.substring(0, 1, 2) == "x"`, filter.ErrInvalidExpression},
		{`name.substring(a.size()) == "x"`, filter.ErrUnsupportedOperation},
		{`name.substring(0) == other.size()`, filter.ErrUnsupportedOperation},
		{`name.substring(0)`, filter.ErrUnsupportedOperation},
		{`a.size() == b.size()`, filter.ErrUnsupportedOperation},
		{`x in y`, filter.ErrInvalidExpression},
		{`x in [y.size()]`, filter.ErrUnsupportedOperation},
		{`a == [1]`, filter.ErrUnsupportedOperation},
		{`"a"`, filter.ErrInvalidExpression},
		{`a && "b"`, filter.ErrInvalidExpression},
		{`!"b"`, filter.ErrInvalidExpression},
		// D3: an ordered comparison with null (or of booleans, which Lua cannot order) is unsupported.
		{`age > null`, filter.ErrUnsupportedOperation},
		{`age <= null`, filter.ErrUnsupportedOperation},
		{`name.size() > null`, filter.ErrUnsupportedOperation},
		{`name.substring(0) < null`, filter.ErrUnsupportedOperation},
		{`active < true`, filter.ErrUnsupportedOperation},
		// size() is a number: a literal of another type never compares with it.
		{`name.size() > "a"`, filter.ErrFieldTypeMismatch},
		{`name.size() == true`, filter.ErrFieldTypeMismatch},
	} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.ErrorIs(t, err, tc.want, tc.expr)
	}
}

func TestLuaTypedLiterals(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	for _, tc := range []struct{ expr, want string }{
		{`at >= timestamp("2024-01-01T00:00:00Z")`, `(type(d["at"]) == "number" and d["at"] >= 1704067200)`},
		{`at < timestamp("2024-01-01T00:00:00.25Z")`, `(type(d["at"]) == "number" and d["at"] < 1704067200.25)`},
		{`at < timestamp("1969-12-31T23:59:59.5Z")`, `(type(d["at"]) == "number" and d["at"] < -0.5)`},
		{`blob == b"a\"\\\x00\xff1"`, `(d["blob"] == "a\034\092\000\2551")`},
	} {
		q, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		assert.Equal(t, tc.want, q, tc.expr)
	}

	q, err := tr.Translate(filtertest.MustParseFilter(t, `blob == b"\x00\xff1"`))
	require.NoError(t, err)

	l := glua.NewState()
	t.Cleanup(l.Close)
	d := l.NewTable()
	d.RawSetString("blob", glua.LString("\x00\xff1"))
	l.SetGlobal("d", d)
	require.NoError(t, l.DoString("result = "+q))
	assert.Equal(t, glua.LTrue, l.GetGlobal("result"), q)
}

func TestLuaTimestampEncoder(t *testing.T) {
	t.Parallel()

	millis := func(v any) (any, error) {
		if ts, ok := v.(time.Time); ok {
			return ts.UnixMilli(), nil
		}

		return v, nil
	}
	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), lua.WithFilterLiteralEncoder(millis), lua.WithFilterTableVar("rec"))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `at >= timestamp("2024-01-01T00:00:00Z")`))
	require.NoError(t, err)
	assert.Equal(t, `(type(rec["at"]) == "number" and rec["at"] >= 1704067200000)`, q)
}

func TestLuaArrayAndDynamicRendering(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags"))
	tr, err := lua.NewFilterTranslator(policy)
	require.NoError(t, err)

	loop := func(params, pv, pe, args string) string {
		return `(function(` + params + `) if type(v) ~= "table" then return ` + pv + ` end for _, e in ipairs(v) do if ` + pe +
			` then return true end end return false end)(` + args + `)`
	}

	for _, tc := range []struct{ expr, want string }{
		{`tags == "red"`, loop("v, x", "(v == x)", "(e == x)", `d["tags"], "red"`)},
		{`tags != other`, "(not " + loop("v, x", "(v == x)", "(e == x)", `d["tags"], d["other"]`) + ")"},
		{`tags < 3`, loop("v, x", `(type(v) == "number" and v < x)`, `(type(e) == "number" and e < x)`, `d["tags"], 3`)},
		{`tags < other`, loop("v, x", `(type(v) == type(x) and (type(v) == "number" or type(v) == "string") and v < x)`,
			`(type(e) == type(x) and (type(e) == "number" or type(e) == "string") and e < x)`, `d["tags"], d["other"]`)},
		{`tags in ["a", b]`, loop("v", `(v == "a" or v == d["b"])`, `(e == "a" or e == d["b"])`, `d["tags"]`)},
		{`"a" in tags`, loop("v, x", "(v == x)", "(e == x)", `d["tags"], "a"`)},
		{`name.startsWith(p)`, `(type(d["name"]) == "string" and type(d["p"]) == "string" and string.sub(d["name"], 1, #d["p"]) == d["p"])`},
		{`name.endsWith(p)`, `(type(d["name"]) == "string" and type(d["p"]) == "string" and (#d["p"] == 0 or string.sub(d["name"], -#d["p"]) == d["p"]))`},
		{`!active`, `(d["active"] == false)`},
		{`!(a == 1)`, `(d["a"] == nil or (type(d["a"]) == "number" and d["a"] ~= 1))`},
		{`has(a.b)`, `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "a", "b") ~= nil)`},
	} {
		q, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		assert.Equal(t, tc.want, q, tc.expr)
	}
}

func TestLuaTableVarOption(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		opts []lua.FilterOption
		want string
	}{
		{nil, `(d["a"] == 1)`},
		{[]lua.FilterOption{lua.WithFilterTableVar("")}, `(d["a"] == 1)`},
		{[]lua.FilterOption{lua.WithFilterTableVar("  row ")}, `(row["a"] == 1)`},
		{[]lua.FilterOption{lua.WithFilterTableVar(new("rec"))}, `(rec["a"] == 1)`},
		{[]lua.FilterOption{lua.WithFilterTableVar((*string)(nil))}, `(d["a"] == 1)`},
	} {
		tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), tc.opts...)
		require.NoError(t, err)
		q, err := tr.Translate(filtertest.MustParseFilter(t, `a == 1`))
		require.NoError(t, err)
		assert.Equal(t, tc.want, q)
	}
}

func TestLuaPredicateRendererAndCollector(t *testing.T) {
	t.Parallel()

	render := func(n filter.Node) (string, bool, error) {
		if ident, ok := n.(*filter.IdentNode); ok && ident.Name == "vip" {
			return `is_vip(d)`, true, nil
		}

		return "", false, nil
	}
	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("vip", "a")), lua.WithFilterPredicateRenderer(render))
	require.NoError(t, err)

	for _, tc := range []struct{ expr, want string }{
		{`vip`, `is_vip(d)`},
		{`!vip`, `(not is_vip(d))`},
		{`!a`, `(d["a"] == false)`},
		{`vip && a == 1`, `(is_vip(d) and (d["a"] == 1))`},
	} {
		q, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		assert.Equal(t, tc.want, q, tc.expr)
	}

	_, err = tr.Translate(filtertest.MustParseFilter(t, `!secret`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	require.ErrorIs(t, tr.ValidateAST(filtertest.MustParseFilter(t, `secret == 1`)), filter.ErrFieldNotAllowed)
}

// TestLuaNullComparisonsRender checks the null comparisons that stay valid: equality with null, and size() equal to null.
func TestLuaNullComparisonsRender(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags")))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want glua.LValue
	}{
		{`age == null`, glua.LTrue},
		{`age != null`, glua.LFalse},
		{`tags == null`, glua.LTrue},
		{`name.size() == null`, glua.LFalse},
	} {
		q, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		assert.Equal(t, tc.want, runLua(t, q, map[string]any{}), tc.expr)
	}
}

// TestLuaFieldKindMismatch checks D2: operands of statically incompatible declared kinds fail at translate time; numeric kinds
// compare with each other and an undeclared kind is not checked.
func TestLuaFieldKindMismatch(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t,
		filter.WithAllowedFields("name", "age", "score", "created", "tags", "other"),
		filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"name": filter.FieldKindString, "age": filter.FieldKindInt, "score": filter.FieldKindFloat,
			"created": filter.FieldKindTimestamp, "tags": filter.FieldKindString,
		}),
	))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want error
	}{
		{`name == age`, filter.ErrFieldTypeMismatch},
		{`age < name`, filter.ErrFieldTypeMismatch},
		{`created > age`, filter.ErrFieldTypeMismatch},
		{`tags == age`, filter.ErrFieldTypeMismatch},
		{`age in [1, name]`, filter.ErrFieldTypeMismatch},
		{`name.size() == name`, filter.ErrFieldTypeMismatch},
		{`age.substring(0) == "1"`, filter.ErrFieldTypeMismatch},
		{`name.substring(0) == age`, filter.ErrFieldTypeMismatch},
		{`age < score`, nil},
		{`tags == name`, nil},
		{`name < other`, nil},
		{`age in [score, 2]`, nil},
		{`name.size() > age`, nil},
		{`name.substring(0, 1) == other`, nil},
	} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		if tc.want == nil {
			require.NoError(t, err, tc.expr)

			continue
		}

		require.ErrorIs(t, err, tc.want, tc.expr)
	}
}

// TestLuaDeniedFieldBeforeShape checks D4: a denied field fails with filter.ErrFieldNotAllowed wherever it appears, also where
// its position is an unsupported operation.
func TestLuaDeniedFieldBeforeShape(t *testing.T) {
	t.Parallel()

	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name", "other")))
	require.NoError(t, err)

	for _, expr := range []string{
		`secret.matches("x")`, `name.contains(secret.size())`, `secret.substring(0)`, `name in [secret.size()]`,
		`name.substring(secret.size()) == "x"`, `name.substring(0) == secret.size()`, `name == [secret]`,
		`name.startsWith(secret.substring(1))`, `secret > null`,
	} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	flua "github.com/altessa-s/go-aip/backend/lua"
	lua "github.com/yuin/gopher-lua"
)

// redisSchema returns the RediSearch schema of fields, panicking on an invalid declaration.
func redisSchema(fields map[string]redis.FieldType) *redis.Schema {
	schema, err := redis.NewSchema(fields)
	if err != nil {
		panic(err)
	}

	return schema
}

func TestRedisNotLiteralBypassesAllowlist(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("public")))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `!"@secret:{classified}"`))
	t.Logf("query=%s error=%v", q, err)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestTranslatorBudgets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, expr string
		options    []filter.PolicyOption
		want       error
	}{
		{"operations", `x == 1`, []filter.PolicyOption{filter.WithMaxOperations(1)}, filter.ErrMaxOperationsExceeded},
		{"list depth", `x in [[[1]]]`, []filter.PolicyOption{filter.WithMaxDepth(2)}, filter.ErrMaxDepthExceeded},
		{"exact budget", `x == 1`, []filter.PolicyOption{filter.WithMaxOperations(3), filter.WithMaxDepth(1)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); checkTranslatorBudgets(t, tc.expr, tc.options, tc.want) })
	}
}

func checkTranslatorBudgets(t *testing.T, expr string, opts []filter.PolicyOption, want error) {
	t.Helper()
	n := filtertest.MustParseFilter(t, expr)

	opts = append(slices.Clip(opts), filter.WithAnyField(true))
	pg, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)
	ch, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)
	ma, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)
	me, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)
	lu, err := flua.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)
	re, err := redis.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)

	cases := []struct {
		name string
		run  func() error
	}{
		{"postgres", func() error { _, _, e := pg.Translate(n); return e }},
		{"clickhouse", func() error { _, _, e := ch.Translate(n); return e }},
		{"mariadb", func() error { _, _, e := ma.Translate(n); return e }},
		{"meili", func() error { _, e := me.Translate(n); return e }},
		{"lua", func() error { _, e := lu.Translate(n); return e }},
		{"redis", func() error { _, e := re.Translate(n); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if want == nil {
				require.NoError(t, tc.run())
			} else {
				require.ErrorIs(t, tc.run(), want)
			}
		})
	}
}

func TestSQLStringFieldArgument(t *testing.T) {
	t.Parallel()

	tr, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	q, args, err := tr.Translate(filtertest.MustParseFilter(t, `a.contains(b)`))
	require.NoError(t, err)
	t.Logf("query=%s args=%#v", q, args)
	require.Empty(t, args, "field reference must not become a bound string literal")
}

func TestLuaBareBoolean(t *testing.T) {
	t.Parallel()

	tr, err := flua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	// A bare boolean holds only for true and its negation only for false (`== false`): a value that is not a bool is unknown
	// (absent, null) or an evaluator error, which matches neither way; Lua truthiness would match "no" both ways.
	for _, tc := range []struct {
		expr, record string
		want         lua.LValue
	}{
		{`active`, `{active = true}`, lua.LTrue},
		{`!active`, `{active = false}`, lua.LTrue},
		{`active`, `{active = "no"}`, lua.LFalse},
		{`!active`, `{active = "no"}`, lua.LFalse},
		{`active`, `{}`, lua.LFalse},
		{`!active`, `{}`, lua.LFalse},
	} {
		q, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err)

		l := lua.NewState()
		require.NoError(t, l.DoString(`d = `+tc.record+`; result = `+q))
		require.Equal(t, tc.want, l.GetGlobal("result"), "query=%s record=%s", q, tc.record)
		l.Close()
	}
}

func TestLuaNULFollowedByDigit(t *testing.T) {
	t.Parallel()

	tr, err := flua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `name == "\x001"`))
	require.NoError(t, err)

	l := lua.NewState()
	t.Cleanup(l.Close)
	d := l.NewTable()
	d.RawSetString("name", lua.LString("\x001"))
	l.SetGlobal("d", d)
	require.NoError(t, l.DoString("result = "+q))
	require.Equal(t, lua.LTrue, l.GetGlobal("result"), "query=%s", q)
}

func TestRedisBackslashEscaping(t *testing.T) {
	t.Parallel()

	got := redis.EscapeTag(`a\|b`)
	t.Logf("escaped=%s", got)
	require.Equal(t, `a\\\|b`, got)
}

func TestLuaUnicodeSize(t *testing.T) {
	t.Parallel()

	tr, err := flua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `name.size() == 1`))
	require.NoError(t, err)

	l := lua.NewState()
	t.Cleanup(l.Close)
	d := l.NewTable()
	d.RawSetString("name", lua.LString("é"))
	l.SetGlobal("d", d)
	require.NoError(t, l.DoString("result = "+q))
	require.Equal(t, lua.LTrue, l.GetGlobal("result"), "query=%s", q)
}

func TestRedisSchemaIsFrozen(t *testing.T) {
	t.Parallel()

	schema := map[string]redis.FieldType{"x": redis.FieldTypeTag}
	tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), redisSchema(schema))
	require.NoError(t, err)
	n := filtertest.MustParseFilter(t, `x == 1`)
	before, err := tr.Translate(n)
	require.NoError(t, err)

	schema["x"] = redis.FieldTypeNumeric
	after, err := tr.Translate(n)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestQueryBackendsRejectDynamicValues(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`a == b`, `a in [b]`, `a.contains(b)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			node := filtertest.MustParseFilter(t, expr)
			me, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
			require.NoError(t, err)
			_, err = me.Translate(node)
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
			re, err := redis.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
			require.NoError(t, err)
			_, err = re.Translate(node)
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
		})
	}
}

func TestLuaFieldOperands(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`active && other`, `a == b`, `a in ["none", b]`, `tags.size() == 2`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			tr, err := flua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
			require.NoError(t, err)
			q, err := tr.Translate(filtertest.MustParseFilter(t, expr))
			require.NoError(t, err)

			l := lua.NewState()
			t.Cleanup(l.Close)
			require.NoError(t, l.DoString(`d = {active = true, other = true, a = "same", b = "same", tags = {1, 2}}; result = `+q))
			require.Equal(t, lua.LTrue, l.GetGlobal("result"), q)
		})
	}
}

func TestSQLDynamicRegexRejected(t *testing.T) {
	t.Parallel()
	node := filtertest.MustParseFilter(t, `name.matches(pattern)`)
	pg, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	_, _, err = pg.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	ch, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	_, _, err = ch.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	ma, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	_, _, err = ma.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestSQLDynamicOperandsHonorFieldPolicy(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`name in [secret]`, `name.contains(secret)`, `name.endsWith(secret)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			tr, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
			require.NoError(t, err)
			_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
		})
	}
}

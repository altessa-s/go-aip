// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// unsafeAttributes are storage names that are no RediSearch attribute: control characters cannot be escaped in a query, and
// white space or an empty segment only reaches a storage name through a client-chosen suffix.
var unsafeAttributes = []string{
	"",
	"b c",
	"zz NOT EXISTS AND secret",
	"a..b",
	".b",
	"b.",
	"a\tb",
	"a\nb",
	"a\x00b",
	"a\x7fb",
	"a\u00a0b",
	"\xff",
}

// mappedTranslator builds a filter translator over the single field x, stored as name, with x declared INDEXMISSING so the
// presence forms reach the renderer.
func mappedTranslator(t *testing.T, name string, ft redis.FieldType) *redis.FilterTranslator {
	t.Helper()

	policy := filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("x"))
	tr, err := redis.NewFilterTranslatorWithSchema(policy, mustSchema(map[string]redis.FieldType{name: ft}, redis.WithSchemaIndexMissing(name)),
		redis.WithFilterOptions(filter.WithFieldMapping(map[string]string{"x": name})))
	require.NoError(t, err)

	return tr
}

// TestFilterTranslatorRejectsUnsafeStorageNames pins SEC-7: a storage name the policy lets through but that is no attribute
// fails before it reaches the query.
func TestFilterTranslatorRejectsUnsafeStorageNames(t *testing.T) {
	t.Parallel()

	exprs := []string{`x == "v"`, `x`, `!x`, `x in ["a", "b"]`, `x == null`, `x != null`, `x.startsWith("a")`}

	for _, name := range unsafeAttributes {
		for _, expr := range exprs {
			t.Run(name+"/"+expr, func(t *testing.T) {
				t.Parallel()

				out, err := mappedTranslator(t, name, redis.FieldTypeTag).Translate(filtertest.MustParseFilter(t, expr))
				require.ErrorIs(t, err, filter.ErrInvalidExpression)
				require.Empty(t, out)
			})
		}
	}
}

// TestFilterTranslatorEscapesFieldNames pins that every storage name is one attribute of the query: RediSearch unescapes a
// field name, so each ASCII character but a letter, a digit and `_` is backslash-escaped.
func TestFilterTranslatorEscapesFieldNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ft   redis.FieldType
		expr string
		want string
	}{
		{"address.city", redis.FieldTypeTag, `x == "Oslo"`, `@address\.city:{Oslo}`},
		{"a-b", redis.FieldTypeNumeric, `x > 3`, `@a\-b:[(3 +inf]`},
		{"$.a", redis.FieldTypeText, `x == "v"`, `@\$\.a:(v)`},
		{"a:b|@c", redis.FieldTypeTag, `x in ["v", "w"]`, `@a\:b\|\@c:{v|w}`},
		{"a{b}", redis.FieldTypeTag, `!x`, `-@a\{b\}:{true}`},
		{"a(b)", redis.FieldTypeTag, `x == null`, `ismissing(@a\(b\))`},
		{"a*", redis.FieldTypeText, `x.startsWith("p")`, `@a\*:p*`},
		{"Straße", redis.FieldTypeTag, `x`, `@Straße:{true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := mappedTranslator(t, tt.name, tt.ft).Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestFilterTranslatorRejectsBacktickFieldNames replays the SEC-7 report: under a prefix allow-list `a.\`b c\` == 1` rendered
// `@a.b c:{1}`. Whether the parser or the core field check refuses it first does not matter; the translator never emits it.
func TestFilterTranslatorRejectsBacktickFieldNames(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	policy, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("a.*")))
	require.NoError(t, err)

	tr, err := redis.NewFilterTranslator(policy)
	require.NoError(t, err)

	for _, expr := range []string{
		"a.`b c` == 1",
		"a.`zz NOT EXISTS AND secret` == \"guess\"",
		"a.`.b` == 1",
		"a.`b.` == 1",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			node, err := parser.Parse(t.Context(), expr)
			if err != nil {
				return // refused by the parser
			}

			out, err := tr.Translate(node)
			require.Error(t, err)
			require.Empty(t, out)
		})
	}

	out, err := tr.Translate(&filter.BinaryOpNode{
		Op:    filter.OpEqual,
		Left:  &filter.IdentNode{Name: "a.b c"},
		Right: &filter.LiteralNode{Value: int64(1)},
	})
	require.Error(t, err)
	require.Empty(t, out)
}

func TestOrderTranslatorRejectsUnsafeStorageNames(t *testing.T) {
	t.Parallel()

	for _, name := range unsafeAttributes {
		if name == "" {
			continue // an empty mapping target is refused by the orderby package itself
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr, err := redis.NewOrderTranslator(orderbytest.MustPolicy(t, orderby.WithAllowedFields("x")),
				redis.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"x": name})))
			require.NoError(t, err)

			got, err := tr.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "x"}}})
			require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
			require.Zero(t, got)
		})
	}
}

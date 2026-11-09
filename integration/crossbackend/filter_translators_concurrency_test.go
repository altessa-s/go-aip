// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestConcurrentCompatibility(t *testing.T) {
	t.Parallel()

	pg, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	ch, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	ma, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	me, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	lu, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	re, err := redis.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "hello"}}

	for name, tr := range map[string]filter.Compatibility{"postgres": pg, "clickhouse": ch, "mariadb": ma, "meili": me, "lua": lu, "redis": re} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for range 12 {
				t.Run("shared", func(t *testing.T) {
					t.Parallel()

					for range 40 {
						require.NoError(t, tr.ValidateAST(n))
					}
				})
			}
		})
	}
}

func TestLiteralEncoder(t *testing.T) {
	t.Parallel()

	encode := func(v any) (any, error) {
		if ts, ok := v.(time.Time); ok {
			return ts.UnixMilli(), nil
		}

		return v, nil
	}
	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), lua.WithFilterLiteralEncoder(encode))
	require.NoError(t, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "ts"}, Right: &filter.LiteralNode{Value: time.UnixMilli(1234)}}
	out, err := tr.Translate(n)
	require.NoError(t, err)
	require.Contains(t, out, "1234")
	require.NoError(t, tr.ValidateAST(n))
}

func TestPredicateRendererAuthorization(t *testing.T) {
	t.Parallel()

	const custom filter.Operator = 250

	handler := func(n filter.Node) (string, bool, error) {
		if call, ok := n.(*filter.CallNode); ok && call.Op == custom {
			return "custom_predicate()", true, nil
		}

		return "", false, nil
	}
	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), lua.WithFilterPredicateRenderer(handler))
	require.NoError(t, err)

	node := &filter.CallNode{Op: custom, Target: &filter.IdentNode{Name: "name"}}
	out, err := tr.Translate(node)
	require.NoError(t, err)
	require.Equal(t, "custom_predicate()", out)

	node.Target = &filter.IdentNode{Name: "secret"}
	_, err = tr.Translate(node)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestNegatedIdentifierUsesRenderer(t *testing.T) {
	t.Parallel()

	render := func(n filter.Node) (string, bool, error) { _, ok := n.(*filter.IdentNode); return "CUSTOM", ok, nil }
	lu, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("active")), lua.WithFilterPredicateRenderer(render))
	require.NoError(t, err)
	me, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("active")), meili.WithFilterPredicateRenderer(render))
	require.NoError(t, err)

	n := &filter.UnaryOpNode{Op: filter.OpNot, Operand: &filter.IdentNode{Name: "active"}}
	for _, tr := range []interface {
		Translate(filter.Node) (string, error)
	}{lu, me} {
		got, err := tr.Translate(n)
		require.NoError(t, err)
		require.Contains(t, got, "CUSTOM")
	}
}

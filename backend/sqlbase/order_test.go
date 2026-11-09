// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func orderContext(tb testing.TB, policy *orderby.Policy, opts ...orderby.TranslatorOption) *orderby.TranslatorContext {
	tb.Helper()

	cfg, err := orderby.NewTranslatorContext(policy, opts...)
	require.NoError(tb, err)

	return cfg
}

func TestTranslateOrder(t *testing.T) {
	t.Parallel()

	cfg := orderContext(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("a", "t.b", "bad")),
		orderby.WithFieldMapping(map[string]string{"bad": "a b"}))
	for _, tc := range []struct {
		name    string
		dialect sqlbase.OrderDialect
		in      string
		want    string
		err     error
	}{
		{name: "postgres", dialect: postgres.OrderDialect(), in: "a desc, t.b", want: `"a" DESC, "t"."b" ASC`},
		{name: "mariadb", dialect: mariadb.OrderDialect(), in: "a desc, t.b", want: "`a` DESC, `t`.`b` ASC"},
		{name: "clickhouse", dialect: clickhouse.OrderDialect(), in: "a desc, t.b", want: "`a` DESC, `t`.`b` ASC"},
		{name: "invalid mapping", dialect: postgres.OrderDialect(), in: "bad", err: orderby.ErrInvalidFieldPath},
		{name: "zero dialect", dialect: sqlbase.OrderDialect{}, in: "a", err: orderby.ErrInvalidFieldPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := sqlbase.TranslateOrder(cfg, tc.dialect, orderbytest.MustParseOrderBy(t, tc.in))
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.want, got)
		})
	}

	got, err := sqlbase.TranslateOrder(cfg, postgres.OrderDialect(), orderby.Spec{})
	require.NoError(t, err)
	require.Empty(t, got)

	strict := orderContext(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("a"), orderby.WithUntrustedInput(true)))
	_, err = sqlbase.TranslateOrder(strict, postgres.OrderDialect(), orderbytest.MustParseOrderBy(t, "a, b"))
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	_, err = sqlbase.TranslateOrder(nil, postgres.OrderDialect(), orderby.Spec{})
	require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
	_, err = sqlbase.TranslateOrder(cfg, nil, orderby.Spec{})
	require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
}

// plainResolver resolves columns without a KeyRenderer.
type plainResolver struct{}

func (plainResolver) Table(name string) (string, error) { return name, nil }

func TestTranslate_AttributesNeedKeyRenderer(t *testing.T) {
	t.Parallel()

	for _, k := range []orderby.Key{{Name: "a", Nulls: orderby.NullsLast}, {Name: "a", Collation: "C"}} {
		_, err := sqlbase.TranslateOrder(orderContext(t, orderbytest.MustPolicy(t)), plainResolver{}, orderby.Spec{Keys: []orderby.Key{k}})
		require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)
	}

	got, err := sqlbase.TranslateOrder(orderContext(t, orderbytest.MustPolicy(t)), plainResolver{}, orderbytest.MustParseOrderBy(t, "a desc"))
	require.NoError(t, err)
	require.Equal(t, "a DESC", got)
}

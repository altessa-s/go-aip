// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderConformance(t *testing.T) {
	t.Parallel()

	type O = orderbytest.Outcome

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := clickhouse.NewOrderTranslator(policy, sqlbase.WithOrderOptions(opts...), sqlbase.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return tr.Translate, nil
		},
		Expect: map[string]O{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "`name` ASC"},
			orderbytest.CaseDescending:        {Want: "`name` DESC"},
			orderbytest.CaseMultipleKeys:      {Want: "`age` DESC, `name` ASC"},
			orderbytest.CaseNestedPath:        {Want: "`address`.`city` ASC"},
			orderbytest.CaseFieldMapping:      {Want: "`created_at` DESC"},
			orderbytest.CasePrefixMapping:     {Want: "`address`.`city` ASC"},
			orderbytest.CaseDefaultOrder:      {Want: "`age` DESC"},
			orderbytest.CaseTiebreaker:        {Want: "`name` ASC, `id` ASC"},
			orderbytest.CaseTiebreakerPresent: {Want: "`id` DESC"},
			orderbytest.CaseAscNullsFirst:     {Want: "`age` ASC NULLS FIRST"},
			orderbytest.CaseAscNullsLast:      {Want: "`age` ASC NULLS LAST"},
			orderbytest.CaseDescNullsFirst:    {Want: "`age` DESC NULLS FIRST"},
			orderbytest.CaseDescNullsLast:     {Want: "`age` DESC NULLS LAST"},
			orderbytest.CaseCollation:         {Want: "`name` ASC COLLATE 'C'"},
		},
	})
}

func TestNewOrderTranslator_NilPolicy(t *testing.T) {
	t.Parallel()

	_, err := clickhouse.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}

// TestOrderTranslator_DottedKeys pins NEW-3: a dotted ORDER BY key a client composed under a wildcard allow-list is refused, as in
// PostgreSQL and MariaDB, instead of being quoted part by part (`a`.`b`.`c`, which may name a joined table); a declared key is
// rendered part by part (the dialect caps each part at 255 bytes, above the parser's field length limit).
func TestOrderTranslator_DottedKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, in string
		allowed  []string
		want     string
		err      error
	}{
		{name: "composed under a wildcard", in: "a.b.c", err: orderby.ErrFieldNotAllowed},
		{name: "two parts under a wildcard", in: "a.b", err: orderby.ErrFieldNotAllowed},
		{name: "long part under a wildcard", in: "a." + strings.Repeat("p", 64), err: orderby.ErrFieldNotAllowed},
		{name: "declared", in: "a.b.c desc", allowed: []string{"a.b.c"}, want: "`a`.`b`.`c` DESC"},
		{name: "plain column", in: "name", want: "`name` ASC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := []orderby.PolicyOption{orderby.WithUntrustedInput(true), orderby.WithFieldAccess(fieldpolicy.NewAllowList("a.*", "name"))}
			if tc.allowed != nil {
				opts = append(opts, orderby.WithAllowedFields(append([]string{"name"}, tc.allowed...)...))
			}

			policy := orderbytest.MustPolicy(t, opts...)

			tr, err := clickhouse.NewOrderTranslator(policy)
			require.NoError(t, err)

			got, err := tr.Translate(orderbytest.MustParseOrderBy(t, tc.in))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Empty(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

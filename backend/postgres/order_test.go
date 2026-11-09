// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderConformance(t *testing.T) {
	t.Parallel()

	type O = orderbytest.Outcome

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := postgres.NewOrderTranslator(policy, sqlbase.WithOrderOptions(opts...), sqlbase.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return tr.Translate, nil
		},
		Expect: map[string]O{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: `"name" ASC`},
			orderbytest.CaseDescending:        {Want: `"name" DESC`},
			orderbytest.CaseMultipleKeys:      {Want: `"age" DESC, "name" ASC`},
			orderbytest.CaseNestedPath:        {Want: `"address"."city" ASC`},
			orderbytest.CaseFieldMapping:      {Want: `"created_at" DESC`},
			orderbytest.CasePrefixMapping:     {Want: `"address"."city" ASC`},
			orderbytest.CaseDefaultOrder:      {Want: `"age" DESC`},
			orderbytest.CaseTiebreaker:        {Want: `"name" ASC, "id" ASC`},
			orderbytest.CaseTiebreakerPresent: {Want: `"id" DESC`},
			orderbytest.CaseAscNullsFirst:     {Want: `"age" ASC NULLS FIRST`},
			orderbytest.CaseAscNullsLast:      {Want: `"age" ASC NULLS LAST`},
			orderbytest.CaseDescNullsFirst:    {Want: `"age" DESC NULLS FIRST`},
			orderbytest.CaseDescNullsLast:     {Want: `"age" DESC NULLS LAST`},
			orderbytest.CaseCollation:         {Want: `"name" COLLATE "C" ASC`},
		},
	})
}

func TestNewOrderTranslator_NilPolicy(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}

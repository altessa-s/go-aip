// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"strings"
	"testing"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderConformance(t *testing.T) {
	t.Parallel()

	unsupported := orderbytest.Outcome{Err: orderby.ErrUnsupportedOrdering}

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := meili.NewOrderTranslator(policy, meili.WithOrderOptions(opts...), meili.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(spec orderby.Spec) (string, error) {
				out, err := tr.Translate(spec)
				return strings.Join(out, ","), err
			}, nil
		},
		Expect: map[string]orderbytest.Outcome{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "name:asc"},
			orderbytest.CaseDescending:        {Want: "name:desc"},
			orderbytest.CaseMultipleKeys:      {Want: "age:desc,name:asc"},
			orderbytest.CaseNestedPath:        {Want: "address.city:asc"},
			orderbytest.CaseFieldMapping:      {Want: "created_at:desc"},
			orderbytest.CasePrefixMapping:     {Want: "address.city:asc"},
			orderbytest.CaseDefaultOrder:      {Want: "age:desc"},
			orderbytest.CaseTiebreaker:        {Want: "name:asc,id:asc"},
			orderbytest.CaseTiebreakerPresent: {Want: "id:desc"},
			orderbytest.CaseAscNullsFirst:     unsupported,
			orderbytest.CaseAscNullsLast:      {Want: "age:asc"},
			orderbytest.CaseDescNullsFirst:    unsupported,
			orderbytest.CaseDescNullsLast:     {Want: "age:desc"},
			orderbytest.CaseCollation:         unsupported,
		},
	})
}

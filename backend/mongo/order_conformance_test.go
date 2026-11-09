// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderConformance(t *testing.T) {
	t.Parallel()

	unsupported := orderbytest.Outcome{Err: orderby.ErrUnsupportedOrdering}

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(opts...), mongo.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(spec orderby.Spec) (string, error) {
				out, err := tr.Translate(spec)

				parts := make([]string, len(out))
				for i, e := range out {
					parts[i] = fmt.Sprintf("%s:%v", e.Key, e.Value)
				}

				return strings.Join(parts, ","), err
			}, nil
		},
		Expect: map[string]orderbytest.Outcome{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "name:1"},
			orderbytest.CaseDescending:        {Want: "name:-1"},
			orderbytest.CaseMultipleKeys:      {Want: "age:-1,name:1"},
			orderbytest.CaseNestedPath:        {Want: "address.city:1"},
			orderbytest.CaseFieldMapping:      {Want: "created_at:-1"},
			orderbytest.CasePrefixMapping:     {Want: "address.city:1"},
			orderbytest.CaseDefaultOrder:      {Want: "age:-1"},
			orderbytest.CaseTiebreaker:        {Want: "name:1,id:1"},
			orderbytest.CaseTiebreakerPresent: {Want: "id:-1"},
			orderbytest.CaseAscNullsFirst:     {Want: "age:1"},
			orderbytest.CaseAscNullsLast:      unsupported,
			orderbytest.CaseDescNullsFirst:    unsupported,
			orderbytest.CaseDescNullsLast:     {Want: "age:-1"},
			orderbytest.CaseCollation:         unsupported,
		},
	})
}

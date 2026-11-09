// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"errors"
	"testing"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderConformance(t *testing.T) {
	t.Parallel()

	unsupported := orderbytest.Outcome{Err: orderby.ErrUnsupportedOrdering}
	tooMany := orderbytest.Outcome{Err: orderby.ErrTooManySortKeys}

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := redis.NewOrderTranslator(policy, redis.WithOrderOptions(opts...), redis.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(spec orderby.Spec) (string, error) {
				out, err := tr.Translate(spec)
				switch {
				case out.Field == "":
					return "", err
				case out.Descending:
					return "SORTBY " + out.Field + " DESC", err
				default:
					return "SORTBY " + out.Field + " ASC", err
				}
			}, nil
		},
		Expect: map[string]orderbytest.Outcome{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "SORTBY name ASC"},
			orderbytest.CaseDescending:        {Want: "SORTBY name DESC"},
			orderbytest.CaseMultipleKeys:      tooMany,
			orderbytest.CaseNestedPath:        {Want: "SORTBY address.city ASC"},
			orderbytest.CaseFieldMapping:      {Want: "SORTBY created_at DESC"},
			orderbytest.CasePrefixMapping:     {Want: "SORTBY address.city ASC"},
			orderbytest.CaseDefaultOrder:      {Want: "SORTBY age DESC"},
			orderbytest.CaseTiebreaker:        tooMany,
			orderbytest.CaseTiebreakerPresent: {Want: "SORTBY id DESC"},
			orderbytest.CaseAscNullsFirst:     unsupported,
			orderbytest.CaseAscNullsLast:      unsupported,
			orderbytest.CaseDescNullsFirst:    unsupported,
			orderbytest.CaseDescNullsLast:     unsupported,
			orderbytest.CaseCollation:         unsupported,
		},
	})
}

// TestTooManySortKeysIsUnsupportedOrdering pins the shared sentinel: the single-key limitation classifies like every other order
// a backend cannot represent.
func TestOrderTooManySortKeysIsUnsupportedOrdering(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewOrderTranslator(orderbytest.MustPolicy(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = tr.Translate(orderbytest.MustParseOrderBy(t, "a, b"))
	if !errors.Is(err, orderby.ErrTooManySortKeys) || !errors.Is(err, orderby.ErrUnsupportedOrdering) {
		t.Fatalf("Translate(a, b) error = %v, want ErrTooManySortKeys wrapping ErrUnsupportedOrdering", err)
	}
}

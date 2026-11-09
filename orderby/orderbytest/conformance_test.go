// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderbytest_test

import (
	"testing"

	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// TestRunConformance runs the suite against the reference backend: the resolved spec of an [orderby.TranslatorContext] in its
// canonical form, with every explicit attribute accepted.
func TestRunConformance(t *testing.T) {
	t.Parallel()

	expect := map[string]orderbytest.Outcome{
		orderbytest.CaseEmpty:             {},
		orderbytest.CaseAscending:         {Want: "name"},
		orderbytest.CaseDescending:        {Want: "name desc"},
		orderbytest.CaseMultipleKeys:      {Want: "age desc, name"},
		orderbytest.CaseNestedPath:        {Want: "address.city"},
		orderbytest.CaseFieldMapping:      {Want: "created_at desc"},
		orderbytest.CasePrefixMapping:     {Want: "address.city"},
		orderbytest.CaseDefaultOrder:      {Want: "age desc"},
		orderbytest.CaseTiebreaker:        {Want: "name, id"},
		orderbytest.CaseTiebreakerPresent: {Want: "id desc"},
		orderbytest.CaseAscNullsFirst:     {Want: "age nulls first"},
		orderbytest.CaseAscNullsLast:      {Want: "age nulls last"},
		orderbytest.CaseDescNullsFirst:    {Want: "age desc nulls first"},
		orderbytest.CaseDescNullsLast:     {Want: "age desc nulls last"},
		orderbytest.CaseCollation:         {Want: `name collate "C"`},
	}

	if got, want := len(orderbytest.ConformanceCases()), len(expect); got != want {
		t.Fatalf("ConformanceCases() has %d names, want %d", got, want)
	}

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			ctx, err := orderby.NewTranslatorContext(policy, opts...)
			if err != nil {
				return nil, err
			}

			return func(spec orderby.Spec) (string, error) {
				resolved, err := ctx.Resolve(spec)
				if err != nil {
					return "", err
				}
				// The reference backend refuses only collations it could not quote safely.
				for _, k := range resolved.Keys {
					if k.Collation != "" && k.Collation != "C" {
						return "", orderby.ErrUnsupportedOrdering
					}
				}

				return resolved.String(), nil
			}, nil
		},
		Expect: expect,
	})

	if len(orderbytest.ConformanceRecords()) != 5 {
		t.Fatal("ConformanceRecords() must hold five records")
	}
}

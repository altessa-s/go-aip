// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderbytest

import (
	"testing"

	"github.com/altessa-s/go-aip/orderby"
)

// MustParseOrderBy creates a no-cache [orderby.Parser] and parses the
// given expression, failing the test on any error. Accepts [testing.TB]
// so it can be called from both tests and benchmarks.
func MustParseOrderBy(tb testing.TB, expr string) orderby.Spec {
	tb.Helper()

	p, err := orderby.NewParser(orderby.WithNoCache(true))
	if err != nil {
		tb.Fatalf("NewParser() error = %v", err)
	}

	ob, err := p.Parse(tb.Context(), expr)
	if err != nil {
		tb.Fatalf("Parse(%q) error = %v", expr, err)
	}

	return ob
}

// MustPolicy builds an [orderby.Policy] from opts and fails the test on a
// construction error.
func MustPolicy(tb testing.TB, opts ...orderby.PolicyOption) *orderby.Policy {
	tb.Helper()

	p, err := orderby.NewPolicy(opts...)
	if err != nil {
		tb.Fatalf("NewPolicy() error = %v", err)
	}

	return p
}

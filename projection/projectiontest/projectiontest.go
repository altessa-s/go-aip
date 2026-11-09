// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projectiontest

import (
	"testing"

	"github.com/altessa-s/go-aip/projection"
)

// MustParse creates a no-cache [projection.Parser] with opts and parses
// expr, failing the test on any error. Accepts [testing.TB] so it can be
// called from both tests and benchmarks.
func MustParse(tb testing.TB, expr string, opts ...projection.ParserOption) projection.Spec {
	tb.Helper()

	p, err := projection.NewParser(append(opts, projection.WithNoCache(true))...)
	if err != nil {
		tb.Fatalf("NewParser() error = %v", err)
	}

	spec, err := p.Parse(tb.Context(), expr)
	if err != nil {
		tb.Fatalf("Parse(%q) error = %v", expr, err)
	}

	return spec
}

// MustPolicy builds a [projection.Policy] from opts and fails the test on a
// construction error.
func MustPolicy(tb testing.TB, opts ...projection.PolicyOption) *projection.Policy {
	tb.Helper()

	p, err := projection.NewPolicy(opts...)
	if err != nil {
		tb.Fatalf("NewPolicy() error = %v", err)
	}

	return p
}

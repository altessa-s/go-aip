// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package decimal128_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/decimal128"
)

func BenchmarkFits(b *testing.B) {
	d := decimal.RequireFromString("-12345.6700")
	for b.Loop() {
		_ = decimal128.Fits(d)
	}
}

func BenchmarkCanonical(b *testing.B) {
	for name, d := range map[string]decimal.Decimal{
		"plain":   decimal.RequireFromString("-12345.6700"),
		"clamped": decimal.New(12, 6140),
	} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				_, _ = decimal128.Canonical(d)
			}
		})
	}
}

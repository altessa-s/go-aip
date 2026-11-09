// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkEvaluateDecimalBounds(b *testing.B) {
	eval, err := newEvaluator(withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})))
	if err != nil {
		b.Fatal(err)
	}

	node := filtertest.MustParseFilter(b, `price >= 100000.5 && price <= 5000000`)

	data := map[string]any{"price": decimal.RequireFromString("1500000.50")}
	for b.Loop() {
		if _, err := eval.Evaluate(node, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecimalLiteral(b *testing.B) {
	b.Run("float", func(b *testing.B) {
		for b.Loop() {
			_, _ = filter.DecimalLiteral(1000000.5)
		}
	})
	b.Run("string", func(b *testing.B) {
		for b.Loop() {
			_, _ = filter.DecimalLiteral("12345678901234567.89")
		}
	})
}

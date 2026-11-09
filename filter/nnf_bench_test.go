// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func BenchmarkNegationNormalForm(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	node, err := p.Parse(b.Context(), `!(rating > 1 || priority == 4) && !(name.startsWith("a"))`)
	if err != nil {
		b.Fatal(err)
	}

	arrays := map[string]bool{"tags": true}

	b.ReportAllocs()

	for b.Loop() {
		_ = filter.NegationNormalForm(node, arrays, map[string]bool{"rating": true, "priority": true})
	}
}

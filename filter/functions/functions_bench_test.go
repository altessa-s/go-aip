// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/functions"
)

func BenchmarkTextSearch(b *testing.B) {
	expand := functions.TextSearch([]string{"name", "description"})
	args := []filter.Node{&filter.LiteralNode{Value: "network"}}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = expand(args)
	}
}

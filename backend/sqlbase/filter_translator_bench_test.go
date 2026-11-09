// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter/filtertest"
)

// The public translator packages benchmark the walk together with their
// dialect. These run it against the stub dialect from translator_test.go,
// whose literal rendering is a constant, so the numbers are dominated by
// the traversal and argument collection rather than by literal
// formatting.

func BenchmarkTranslate_Simple(b *testing.B) {
	node := filtertest.MustParseFilter(b, `name == "John"`)
	trans := mustTranslator(b, failing{})

	for b.Loop() {
		_, _, _ = trans.Translate(node)
	}
}

func BenchmarkTranslate_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	trans := mustTranslator(b, failing{})

	for b.Loop() {
		_, _, _ = trans.Translate(node)
	}
}

func BenchmarkTranslateInline_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	trans := mustTranslator(b, failing{})

	for b.Loop() {
		_, _ = trans.TranslateInline(node)
	}
}

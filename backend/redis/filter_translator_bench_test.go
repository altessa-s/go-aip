// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkFilterTranslate_Simple(b *testing.B) {
	node := filtertest.MustParseFilter(b, `status == 1`)
	trans := mustTranslator(b, testSchema)

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslate_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(status == 1 && priority >= 3) && (failures == 0 && age > 10)`)
	trans := mustTranslator(b, testSchema)

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslate_FieldMapping(b *testing.B) {
	node := filtertest.MustParseFilter(b, `mapped_status == 1`)
	trans := mustTranslator(b,
		map[string]FieldType{"mapped_status": FieldTypeNumeric},
		filter.WithFieldMapping(map[string]string{"status": "mapped_status"}),
	)

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterEscapeTag(b *testing.B) {
	const value = "task-2026.10.29 retry:1|ops@example.com"

	b.ReportAllocs()

	for b.Loop() {
		_ = EscapeTag(value)
	}
}

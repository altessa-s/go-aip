// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkTranslate_Simple(b *testing.B) {
	node := filtertest.MustParseFilter(b, `name == "John"`)
	trans := mustTranslator(b, "")

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkTranslate_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	trans := mustTranslator(b, "")

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkTranslate_FieldMapping(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`organizationIds in ["org-1", "org-2"] && status == 2`)
	trans := mustTranslator(b, "row", filter.WithFieldMapping(map[string]string{
		"organizationIds": "organization_id",
	}))

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

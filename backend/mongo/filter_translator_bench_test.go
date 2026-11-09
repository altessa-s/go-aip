// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkFilterTranslate_Simple(b *testing.B) {
	node := filtertest.MustParseFilter(b, `name == "John"`)
	trans := mustTranslator(b)

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslate_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	trans := mustTranslator(b)

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslate_FieldMapping(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`organizationIds in ["org-1", "org-2"] && status == 2`)
	trans := mustTranslator(b, filter.WithFieldMapping(map[string]string{
		"organizationIds": "organization_id",
	}))

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslate_ZeroWhenAbsent(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`description == "" && !oneShot && nextRunAt < 100 && description.matches("^a")`)
	trans := mustTranslatorWithSchema(b, []SchemaOption{WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
		"description": filter.FieldKindString,
		"oneShot":     filter.FieldKindBool,
		"nextRunAt":   filter.FieldKindInt,
	})})

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

func BenchmarkFilterTranslateD_Complex(b *testing.B) {
	node := filtertest.MustParseFilter(b,
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	trans := mustTranslator(b)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = trans.TranslateD(node)
	}
}

func BenchmarkFilterTranslate_ArraySize(b *testing.B) {
	node := filtertest.MustParseFilter(b, `"red" in tags && tags.size() == 2`)
	trans := mustTranslator(b, filter.WithArrayFields("tags"))

	b.ReportAllocs()

	for b.Loop() {
		_, _ = trans.Translate(node)
	}
}

// BenchmarkFilterTranslate_ZeroWhenAbsentMatches translates matches() on a field declared by WithSchemaZeroWhenAbsent, whose pattern the
// translator tests against the stored zero (the empty string).
func BenchmarkFilterTranslate_ZeroWhenAbsentMatches(b *testing.B) {
	node := filtertest.MustParseFilter(b, `note.matches("^(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\\.[a-z]{2,}$")`)
	// The pattern holds three unbounded repetitions, beyond the safe subset: a service pattern, not a client's.
	trans := mustTranslatorWithSchema(b, []SchemaOption{
		WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindString}),
	}, filter.WithUnsafeRegex(true))
	b.ReportAllocs()

	for b.Loop() {
		if _, err := trans.Translate(node); err != nil {
			b.Fatal(err)
		}
	}
}

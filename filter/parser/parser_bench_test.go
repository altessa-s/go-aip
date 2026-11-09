// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"

	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func BenchmarkParse_Simple(b *testing.B) {
	p, err := filterparser.New(filterparser.WithNoCache(true))
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for b.Loop() {
		_, _ = p.Parse(b.Context(), `name == "John"`)
	}
}

func BenchmarkParse_Complex(b *testing.B) {
	p, err := filterparser.New(filterparser.WithNoCache(true))
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for b.Loop() {
		_, _ = p.Parse(b.Context(), `(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`)
	}
}

func BenchmarkParse_Cached(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	expr := `(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`
	// Warm up cache
	_, _ = p.Parse(b.Context(), expr)

	b.ResetTimer()

	for b.Loop() {
		_, _ = p.Parse(b.Context(), expr)
	}
}

func BenchmarkParseShared_Cached(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	expr := `(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`
	// Warm up cache
	_, _ = p.ParseShared(b.Context(), expr)

	b.ResetTimer()

	for b.Loop() {
		_, _ = p.ParseShared(b.Context(), expr)
	}
}

func BenchmarkParseCustomFunction(b *testing.B) {
	p, err := filterparser.New(
		filterparser.WithNoCache(true),
		filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
			"createdAfter": filterfuncs.CompareField("createdAt", filter.OpGT),
			"updatedAfter": filterfuncs.CompareField("updatedAt", filter.OpGT),
		}),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for b.Loop() {
		_, _ = p.Parse(b.Context(), `createdAfter("2024-01-01") && updatedAfter("2024-01-02")`)
	}
}

func BenchmarkParse_TextSearch(b *testing.B) {
	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
		"search": filterfuncs.TextSearch([]string{"code", "name", "description"}),
	}))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := p.Parse(b.Context(), `search("developer") && grade == "senior"`); err != nil {
			b.Fatal(err)
		}
	}
}

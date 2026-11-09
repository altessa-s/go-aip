// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser_test

import (
	"testing"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

const benchExpression = `(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"]`

func BenchmarkParse_CachedParallel(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	_, _ = p.Parse(b.Context(), benchExpression)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.Parse(b.Context(), benchExpression); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkParseShared_CachedParallel(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	_, _ = p.ParseShared(b.Context(), benchExpression)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.ParseShared(b.Context(), benchExpression); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkParse_CachedFailureParallel(b *testing.B) {
	p, err := filterparser.New()
	if err != nil {
		b.Fatal(err)
	}

	const invalid = `name == (`

	_, _ = p.ParseShared(b.Context(), invalid)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.ParseShared(b.Context(), invalid); err == nil {
				b.Error("want a parse error")
				return
			}
		}
	})
}

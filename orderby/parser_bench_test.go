// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/altessa-s/go-aip/orderby"
)

const benchExpr = "create_time desc, organization_id asc, slug, address.city desc, updated_at"

func BenchmarkParse_Cached(b *testing.B) {
	p, err := orderby.NewParser()
	if err != nil {
		b.Fatal(err)
	}

	ctx := b.Context()

	for b.Loop() {
		_, _ = p.Parse(ctx, benchExpr)
	}
}

func BenchmarkParse_NoCache(b *testing.B) {
	p, err := orderby.NewParser(orderby.WithNoCache(true))
	if err != nil {
		b.Fatal(err)
	}

	ctx := b.Context()

	for b.Loop() {
		_, _ = p.Parse(ctx, benchExpr)
	}
}

func BenchmarkParse_SingleKey(b *testing.B) {
	p, err := orderby.NewParser(orderby.WithNoCache(true))
	if err != nil {
		b.Fatal(err)
	}

	ctx := b.Context()

	for b.Loop() {
		_, _ = p.Parse(ctx, "create_time desc")
	}
}

func BenchmarkSpec_String(b *testing.B) {
	p, err := orderby.NewParser()
	if err != nil {
		b.Fatal(err)
	}

	spec, err := p.Parse(b.Context(), benchExpr)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		_ = spec.String()
	}
}

func BenchmarkSpec_Reverse(b *testing.B) {
	p, err := orderby.NewParser()
	if err != nil {
		b.Fatal(err)
	}

	spec, err := p.Parse(b.Context(), benchExpr)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		_ = spec.Reverse()
	}
}

func BenchmarkParse_CachedParallel(b *testing.B) {
	p, err := orderby.NewParser()
	if err != nil {
		b.Fatal(err)
	}

	_, _ = p.Parse(b.Context(), benchExpr)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.Parse(b.Context(), benchExpr); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

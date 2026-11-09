// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/altessa-s/go-aip/projection"
)

const benchExpression = "name, email, address.city, address.zip, createTime, author.profile.displayName"

func BenchmarkParseCached(b *testing.B) {
	p := newParser(b)

	ctx := b.Context()
	for b.Loop() {
		_, _ = p.Parse(ctx, benchExpression)
	}
}

func BenchmarkParseNoCache(b *testing.B) {
	p := newParser(b, projection.WithNoCache(true))

	ctx := b.Context()
	for b.Loop() {
		_, _ = p.Parse(ctx, benchExpression)
	}
}

func BenchmarkResolve(b *testing.B) {
	ctx, err := newContext(
		projection.WithAllowedFields("name", "email", "address.*", "createTime", "author.*"),
		projection.WithDeniedFields("author.profile.secret"),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("_id"),
	)
	if err != nil {
		b.Fatal(err)
	}

	spec := newParser(b).MustParse(benchExpression)
	for b.Loop() {
		_, _ = ctx.Resolve(spec)
	}
}

func BenchmarkParseCached_Parallel(b *testing.B) {
	p := newParser(b)

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

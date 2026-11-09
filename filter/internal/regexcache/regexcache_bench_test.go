// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter/internal/regexcache"
)

func BenchmarkCompile_Hit(b *testing.B) {
	cache, err := regexcache.New(256)
	if err != nil {
		b.Fatal(err)
	}

	const pattern = `^(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`
	if _, err = cache.Compile(pattern); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err = cache.Compile(pattern); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookup_HitSafe(b *testing.B) {
	cache, err := regexcache.New(256)
	if err != nil {
		b.Fatal(err)
	}

	const pattern = `^(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`
	if _, err := cache.Lookup(pattern); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		p, err := cache.Lookup(pattern)
		if err != nil {
			b.Fatal(err)
		}

		_ = p.Safe()
	}
}

func BenchmarkSafe(b *testing.B) {
	const pattern = `^(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`

	b.ReportAllocs()

	for b.Loop() {
		_ = regexcache.Safe(pattern)
	}
}

func BenchmarkLookup_HitParallel(b *testing.B) {
	cache, err := regexcache.New(256)
	if err != nil {
		b.Fatal(err)
	}

	const pattern = `^(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`
	if _, err := cache.Lookup(pattern); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := cache.Lookup(pattern); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

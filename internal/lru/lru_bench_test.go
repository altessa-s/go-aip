// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lru_test

import (
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/altessa-s/go-aip/internal/lru"
)

const benchKeys = 512

func benchCache(b *testing.B) (*lru.Cache[string, int], []string) {
	b.Helper()

	c, err := lru.NewCache[string, int](1024)
	if err != nil {
		b.Fatal(err)
	}

	keys := make([]string, benchKeys)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
		c.Put(keys[i], i)
	}

	return c, keys
}

func BenchmarkCache_Get(b *testing.B) {
	c, keys := benchCache(b)

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		if _, ok := c.Get(keys[i%benchKeys]); !ok {
			b.Fatal("miss")
		}

		i++
	}
}

func BenchmarkCache_GetParallel(b *testing.B) {
	c, keys := benchCache(b)

	var next atomic.Int64

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := int(next.Add(97))
		for pb.Next() {
			if _, ok := c.Get(keys[i%benchKeys]); !ok {
				b.Error("miss")
				return
			}

			i++
		}
	})
}

func BenchmarkCache_GetParallelHotKey(b *testing.B) {
	c, keys := benchCache(b)
	hot := keys[0]

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, ok := c.Get(hot); !ok {
				b.Error("miss")
				return
			}
		}
	})
}

func BenchmarkCache_PutChurn(b *testing.B) {
	c, err := lru.NewCache[string, int](1024)
	if err != nil {
		b.Fatal(err)
	}

	keys := make([]string, 4096)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		c.Put(keys[i%len(keys)], i)
		i++
	}
}

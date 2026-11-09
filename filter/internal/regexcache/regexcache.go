// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache

import (
	"regexp"
	"sync/atomic"

	"github.com/altessa-s/go-aip/internal/lru"
)

// MaxSize is the number of compiled patterns the cache keeps.
const MaxSize = 256

// Cache owns a bounded working set and its counters. A zero size disables caching.
type Cache struct {
	cache  lru.Cacher[string, *Pattern]
	hits   atomic.Uint64
	misses atomic.Uint64
}

// New constructs an isolated cache. Zero disables caching.
func New(size int) (*Cache, error) {
	c := &Cache{}
	if size == 0 {
		return c, nil
	}

	var err error

	c.cache, err = lru.NewCache[string, *Pattern](size)

	return c, err
}

// Pattern is a compiled pattern and, once asked for, the verdict of [Safe] on it.
type Pattern struct {
	re     *regexp.Regexp
	source string
	safety atomic.Pointer[verdict]
}

// verdict is the outcome of [Safe] on a pattern.
type verdict struct{ err error }

// Regexp returns the compiled pattern.
func (p *Pattern) Regexp() *regexp.Regexp { return p.re }

// Safe returns [Safe] of the pattern, computed on the first call and kept with the pattern.
func (p *Pattern) Safe() error {
	if v := p.safety.Load(); v != nil {
		return v.err
	}

	v := &verdict{err: Safe(p.source)}
	p.safety.Store(v)

	return v.err
}

// Compile returns pattern compiled as RE2 ([regexp.Compile]), from the cache when it holds it. A pattern that does not compile
// is not cached.
func (c *Cache) Compile(pattern string) (*regexp.Regexp, error) {
	p, err := c.Lookup(pattern)
	if err != nil {
		return nil, err
	}

	return p.re, nil
}

// Lookup returns pattern compiled as [Compile] does, with its [Pattern.Safe] verdict.
func (c *Cache) Lookup(pattern string) (*Pattern, error) {
	if c.cache != nil {
		if p, ok := c.cache.Get(pattern); ok {
			c.hits.Add(1)
			return p, nil
		}
	}

	c.misses.Add(1)

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	p := &Pattern{re: re, source: pattern}
	if c.cache != nil {
		c.cache.Put(pattern, p)
	}

	return p, nil
}

// Stats returns the hits and misses counted since the last [ResetStats] and the number of cached patterns.
func (c *Cache) Stats() (hitCount, missCount uint64, size int) {
	size = 0
	if c.cache != nil {
		size = c.cache.Len()
	}

	return c.hits.Load(), c.misses.Load(), size
}

// ResetStats zeroes the hit and miss counters without touching the cached patterns.
func (c *Cache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import "sync"

// patternCache is a bounded, concurrency-safe string memo. When it is full,
// a put of a new key drops every entry and starts over: the cached values are
// pure functions of their keys, so a reset costs only recomputation, and the
// bound holds whatever keys a client sends. The module keeps its own cache
// rather than importing the root module's internal packages, which carry no
// compatibility promise across module versions.
type patternCache struct {
	entries map[string]string
	mu      sync.RWMutex
	size    int
}

// newPatternCache returns a cache holding at most size entries.
func newPatternCache(size int) *patternCache {
	return &patternCache{entries: make(map[string]string, size), size: size}
}

// get returns the cached value of key.
func (c *patternCache) get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.entries[key]

	return v, ok
}

// put caches value under key, first emptying a full cache.
func (c *patternCache) put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.size {
		clear(c.entries)
	}

	c.entries[key] = value
}

// count returns the number of cached entries.
func (c *patternCache) count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package lru is the size-bounded, concurrency-safe cache the parsers and the
// descriptor lookups memoize their results in, with a singleflight-deduplicated
// [Cache.GetOrCompute].
//
// The cache is read-mostly: a hit takes no lock and writes no shared memory once
// the entry is marked referenced, so lookups scale with cores. Entries live in
// sharded, immutable maps that a writer replaces copy-on-write; eviction is
// CLOCK (second chance), an approximation of least-recently-used.
package lru

import (
	"context"
	"errors"
	"hash/maphash"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/altessa-s/go-atlas-core/runtime/singleflight"
)

const (
	// minShardEntries is the capacity per shard below which the cache does not
	// split further: a write copies one shard map, so this bounds that copy for
	// small caches while keeping one shard for tiny ones.
	minShardEntries = 16
	// maxShards bounds the number of shard maps.
	maxShards = 256
	// maxInitialRing bounds the ring capacity reserved up front for a large cache.
	maxInitialRing = 1024
)

// errNonPositiveSize is returned for a size that is not positive.
var errNonPositiveSize = errors.New("must provide a positive size")

// Cacher is the part of an LRU cache the parsers use.
type Cacher[K comparable, V any] interface {
	// Get returns the value for key and true if found, or the zero value and
	// false.
	Get(key K) (value V, ok bool)
	// Put adds or updates key with value and reports whether an entry was
	// evicted.
	Put(key K, value V) (evicted bool)
	// Len returns the number of entries in the cache.
	Len() int
	// GetOrCompute returns the cached value for key, or computes and stores
	// it on a miss; concurrent computations for the same key are
	// deduplicated.
	GetOrCompute(ctx context.Context, key K, fn func(ctx context.Context) (V, error)) (V, error)
}

// Cache is a thread-safe cache holding at most a fixed number of entries and,
// when built with [NewWeightedCache], at most a fixed total cost.
//
// Reads load an immutable shard map atomically and set the entry's referenced
// bit only when it is clear. Writes serialize on one mutex, copy the shard maps
// they change and publish them; eviction sweeps a CLOCK hand over the entries,
// clearing referenced bits and evicting the first entry found without one.
type Cache[K comparable, V any] struct {
	group  *singleflight.Group[K, V]
	seed   maphash.Seed
	shards []shard[K, V]
	mask   uint64

	mu         sync.Mutex
	ring       []*entry[K, V] // the CLOCK; a nil slot is a hole, listed in holes
	holes      []int          // free ring slots, the most recently freed last
	live       int
	hand       int
	cost       int
	maxEntries int
	maxCost    int // 0: no cost bound
}

// shard holds an immutable map from key to entry, replaced as a whole by
// writers.
type shard[K comparable, V any] struct {
	m atomic.Pointer[map[K]*entry[K, V]]
}

// entry is one cached value. key, value, cost and shard never change after the
// entry is published; slot is guarded by Cache.mu.
type entry[K comparable, V any] struct {
	key        K
	value      V
	cost       int
	shard      uint64
	slot       int
	referenced atomic.Bool
}

// NewCache creates a cache holding at most size entries; size must be
// positive.
func NewCache[K comparable, V any](size int) (*Cache[K, V], error) {
	return newCache[K, V](size, 0)
}

// NewWeightedCache creates a cache holding at most size entries whose costs,
// as given to [Cache.PutWithCost], add up to at most maxCost; both must be
// positive.
func NewWeightedCache[K comparable, V any](size, maxCost int) (*Cache[K, V], error) {
	if maxCost <= 0 {
		return nil, errors.New("must provide a positive cost bound")
	}

	return newCache[K, V](size, maxCost)
}

func newCache[K comparable, V any](size, maxCost int) (*Cache[K, V], error) {
	if size <= 0 {
		return nil, errNonPositiveSize
	}

	n := 1
	for n < maxShards && n*minShardEntries < size {
		n *= 2
	}

	c := &Cache[K, V]{
		group:      singleflight.New[K, V](singleflight.WithMaxConcurrent(0)),
		seed:       maphash.MakeSeed(),
		shards:     make([]shard[K, V], n),
		mask:       uint64(n - 1),
		ring:       make([]*entry[K, V], 0, min(size, maxInitialRing)),
		maxEntries: size,
		maxCost:    maxCost,
	}

	for i := range c.shards {
		c.shards[i].m.Store(new(make(map[K]*entry[K, V])))
	}

	return c, nil
}

func (c *Cache[K, V]) shardIndex(key K) uint64 {
	if c.mask == 0 {
		return 0
	}

	return maphash.Comparable(c.seed, key) & c.mask
}

// Get returns the value for key and true if found, or the zero value and
// false. A hit marks the entry as referenced, so the next eviction sweep
// spares it.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	e, ok := (*c.shards[c.shardIndex(key)].m.Load())[key]
	if !ok {
		var zero V
		return zero, false
	}

	if !e.referenced.Load() {
		e.referenced.Store(true)
	}

	return e.value, true
}

// Put adds or updates key with value. It reports whether an entry was evicted
// to make room.
func (c *Cache[K, V]) Put(key K, value V) bool {
	return c.PutWithCost(key, value, 0)
}

// PutWithCost adds or updates key with value at the given cost, evicting
// entries until both bounds hold, and reports whether an entry was evicted. A
// value whose cost alone exceeds the cache's cost bound is not stored, and an
// older value under key is dropped.
func (c *Cache[K, V]) PutWithCost(key K, value V, cost int) bool {
	idx := c.shardIndex(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	var w writeSet[K, V]

	old, exists := (*c.shards[idx].m.Load())[key]
	if exists {
		c.remove(old, &w)
	}

	evicted := false

	if c.maxCost == 0 || cost <= c.maxCost {
		for c.live > 0 && (c.live >= c.maxEntries || (c.maxCost > 0 && c.cost+cost > c.maxCost)) {
			c.evictOne(&w)

			evicted = true
		}

		e := &entry[K, V]{key: key, value: value, cost: cost, shard: idx}
		// An update was just used; a new entry earns its referenced bit on its first hit.
		e.referenced.Store(exists)
		c.insert(e)
		w.edit(c, idx)[key] = e
	}

	w.publish(c)

	return evicted
}

// insert places e in the most recently freed ring slot, just behind the hand
// after an eviction, or appends it when the ring has no hole.
func (c *Cache[K, V]) insert(e *entry[K, V]) {
	if n := len(c.holes); n > 0 {
		e.slot = c.holes[n-1]
		c.holes = c.holes[:n-1]
		c.ring[e.slot] = e
	} else {
		e.slot = len(c.ring)
		c.ring = append(c.ring, e)
	}

	c.live++
	c.cost += e.cost
}

// evictOne advances the CLOCK hand, clearing referenced bits, and evicts the
// first entry it finds unreferenced; the cache must not be empty.
func (c *Cache[K, V]) evictOne(w *writeSet[K, V]) {
	for {
		if c.hand >= len(c.ring) {
			c.hand = 0
		}

		e := c.ring[c.hand]
		c.hand++

		switch {
		case e == nil:
		case e.referenced.Load():
			e.referenced.Store(false)
		default:
			c.remove(e, w)
			return
		}
	}
}

// remove frees e's ring slot and deletes it from its shard map.
func (c *Cache[K, V]) remove(e *entry[K, V], w *writeSet[K, V]) {
	c.ring[e.slot] = nil
	c.holes = append(c.holes, e.slot)
	c.live--
	c.cost -= e.cost
	delete(w.edit(c, e.shard), e.key)
}

// Len returns the number of entries in the cache.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.live
}

// Cost returns the total cost of the cached entries.
func (c *Cache[K, V]) Cost() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.cost
}

// GetOrCompute returns the cached value for key, or calls fn once (through
// singleflight) to compute and cache the result on a miss; an error is
// returned and not cached. Callers cancel independently; a shared computation
// receives a context bounded by the singleflight default timeout (15 seconds).
func (c *Cache[K, V]) GetOrCompute(ctx context.Context, key K, fn func(ctx context.Context) (V, error)) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	return c.group.Do(ctx, key, func(work context.Context) (V, error) {
		if v, ok := c.Get(key); ok {
			return v, nil
		}

		v, err := fn(work)
		if err == nil {
			c.Put(key, v)
		}

		return v, err
	})
}

// writeSet collects the shard maps one write changes: each is copied once,
// edited, and published when the write completes.
type writeSet[K comparable, V any] struct {
	edits []shardEdit[K, V]
}

type shardEdit[K comparable, V any] struct {
	idx uint64
	m   map[K]*entry[K, V]
}

// edit returns the private copy of shard idx, copying the published map on
// first use.
func (w *writeSet[K, V]) edit(c *Cache[K, V], idx uint64) map[K]*entry[K, V] {
	for _, e := range w.edits {
		if e.idx == idx {
			return e.m
		}
	}

	m := maps.Clone(*c.shards[idx].m.Load())
	w.edits = append(w.edits, shardEdit[K, V]{idx: idx, m: m})

	return m
}

func (w *writeSet[K, V]) publish(c *Cache[K, V]) {
	for _, e := range w.edits {
		c.shards[e.idx].m.Store(&e.m)
	}
}

var _ Cacher[string, any] = (*Cache[string, any])(nil)

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"context"
	"hash/maphash"
	"time"

	"github.com/altessa-s/go-atlas-core/runtime/singleflight"

	"github.com/altessa-s/go-aip/internal/lru"

	filter "github.com/altessa-s/go-aip/filter"
)

// Cost model of a cached AST, in approximate bytes of live heap. It does not need to be exact: it only has to grow with
// what an entry really holds, so the byte budget bounds the cache's memory to within a small factor.
const (
	// nodeCost covers a node's struct, its size-class rounding and the interface slot its parent holds it in.
	nodeCost = 64
	// entryCost covers an entry's map slot, ring slot and bookkeeping.
	entryCost = 128
	// timeCost is the size of a time.Time literal value.
	timeCost = 24
	// maxEntryShare is the fraction of the byte budget one entry may take: a larger AST is returned but not cached, so a
	// single huge filter cannot flush the whole cache.
	maxEntryShare = 16
	// failureCacheSize bounds the number of entries of the negative cache of deterministic parse failures.
	failureCacheSize = 128
	// failureCacheBytes bounds the bytes of error text the negative cache holds, entryCost per entry included.
	failureCacheBytes = 64 << 10
	// maxFailureBytes is the longest error text the negative cache keeps; a longer error, such as a custom function's that
	// echoes its input, is returned but not cached. A parse error is far shorter ([parseError]).
	maxFailureBytes = 1 << 10
)

// astCache memoizes parsed ASTs in a cache bounded both by entry count and by the approximate bytes the ASTs hold, and
// remembers recent deterministic parse failures in a small negative cache keyed by a seeded hash of the expression and
// bounded by entry count and by the bytes of the error texts.
// Concurrent misses for one expression are deduplicated. Lookups take no lock (see [lru.Cache]). It is safe for
// concurrent use.
type astCache struct {
	group    *singleflight.Group[string, filter.Node]
	seed     maphash.Seed
	entries  *lru.Cache[string, astEntry]
	failures *lru.Cache[failureKey, error]

	maxEntryBytes int
}

// astEntry is a cached AST and the plan [Parser.Parse] copies it with; a nil plan falls back to [filter.Clone].
type astEntry struct {
	node filter.Node
	plan *clonePlan
}

// failureKey identifies an expression by a per-cache seeded 64-bit hash and its length. The seed is random, so a client
// cannot aim a collision at another expression, and a chance collision (about 2^-64 per lookup) would only report a
// cached parse error for an expression of the same length.
type failureKey struct {
	hash   uint64
	length int
}

// cached is a cache hit: an AST with its copy plan, or the error of a failed parse.
type cached struct {
	astEntry

	err error
}

// newASTCache returns a cache holding at most maxEntries ASTs and about maxBytes bytes of them; both must be positive.
func newASTCache(maxEntries, maxBytes int) (*astCache, error) {
	entries, err := lru.NewWeightedCache[string, astEntry](maxEntries, maxBytes)
	if err != nil {
		return nil, err
	}

	failures, err := lru.NewWeightedCache[failureKey, error](failureCacheSize, failureCacheBytes)
	if err != nil {
		return nil, err
	}

	return &astCache{
		group:         singleflight.New[string, filter.Node](singleflight.WithMaxConcurrent(0)),
		seed:          maphash.MakeSeed(),
		entries:       entries,
		failures:      failures,
		maxEntryBytes: max(maxBytes/maxEntryShare, 1),
	}, nil
}

// getOrParse returns the cached AST or parse failure of expression, or runs parse once for concurrent callers and caches
// its result: the AST when it fits the byte budget, the error in the negative cache. A caller's own context error is
// returned but never cached, since it says nothing about the expression.
func (c *astCache) getOrParse(ctx context.Context, expression string, parse func() (filter.Node, error)) (filter.Node, error) {
	if hit, ok := c.get(expression); ok {
		return hit.node, hit.err
	}

	return c.group.Do(ctx, expression, func(context.Context) (filter.Node, error) {
		if hit, ok := c.get(expression); ok {
			return hit.node, hit.err
		}

		node, err := parse()
		if err != nil {
			c.putFailure(expression, err)
			return nil, err
		}

		c.put(expression, node)

		return node, nil
	})
}

// get looks expression up in both caches; ok reports a hit, of an AST or of a failure.
func (c *astCache) get(expression string) (cached, bool) {
	if e, ok := c.entries.Get(expression); ok {
		return cached{astEntry: e}, true
	}

	if err, ok := c.failures.Get(c.failureKey(expression)); ok {
		return cached{err: err}, true
	}

	return cached{}, false
}

// put caches node under expression unless it exceeds the per-entry share of the byte budget; the cache evicts entries
// until both bounds hold.
func (c *astCache) put(expression string, node filter.Node) {
	cost := entryCost + len(expression) + astCost(node)
	if cost > c.maxEntryBytes {
		return
	}

	c.entries.PutWithCost(expression, astEntry{node: node, plan: newClonePlan(node)}, cost)
}

// putFailure remembers a parse failure of expression unless its text exceeds maxFailureBytes; the negative cache evicts
// until both of its bounds hold.
func (c *astCache) putFailure(expression string, err error) {
	size := len(err.Error())
	if size > maxFailureBytes {
		return
	}

	c.failures.PutWithCost(c.failureKey(expression), err, entryCost+size)
}

func (c *astCache) failureKey(expression string) failureKey {
	return failureKey{hash: maphash.String(c.seed, expression), length: len(expression)}
}

// stats returns the number of cached ASTs, their approximate bytes and the number of cached failures.
func (c *astCache) stats() (entries, bytes, failures int) {
	return c.entries.Len(), c.entries.Cost(), c.failures.Len()
}

// astCost estimates the bytes an AST holds: a fixed cost per node plus the bytes of its names and literal payloads.
func astCost(root filter.Node) int {
	cost := 0

	filter.Walk(root, func(n filter.Node) bool {
		cost += nodeCost

		switch n := n.(type) {
		case *filter.IdentNode:
			cost += len(n.Name)
		case *filter.LiteralNode:
			cost += len(n.Text)

			switch v := n.Value.(type) {
			case string:
				cost += len(v)
			case []byte:
				cost += len(v)
			case time.Time:
				cost += timeCost
			}
		}

		return true
	})

	return cost
}

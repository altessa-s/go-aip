# lru

```go
import "github.com/altessa-s/go-aip/internal/lru"
```

The size-bounded, concurrency-safe cache the parsers and the descriptor lookups memoize their results in, with a singleflight-deduplicated
`GetOrCompute`. Used by [filter/parser](../../filter/parser), [orderby](../../orderby), [projection](../../projection), [fieldinfo](../../fieldinfo),
the annotation caches of [internal/behavior](../behavior) and the regex cache of [filter/internal/regexcache](../../filter/internal/regexcache).
Internal to the module.

## Design

The cached values are immutable and read far more often than written, so the cache is read-mostly:

- Entries live in sharded maps (a power of two, one shard per 16 entries of capacity, at most 256) that are never modified in place. A lookup loads
  the shard map atomically and indexes it: no lock, and no shared write once the entry's referenced bit is set, so hits scale with cores.
- Writes serialize on one mutex, copy the shard maps they change and publish the copies.
- Eviction is CLOCK (second chance): a hit sets the entry's referenced bit; the sweep clears set bits and evicts the first entry found without
  one. It approximates least-recently-used; a new entry earns its bit on its first hit.
- The bounds are exact and global: at most `size` entries and, for a weighted cache, at most `maxCost` total cost.

## Key symbols

| Symbol                               | Purpose                                                                                                                                                  |
|--------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Cacher[K, V]`                       | The part of the cache the parsers use: `Get`, `Put`, `Len`, `GetOrCompute`                                                                               |
| `Cache[K, V]`                        | Thread-safe cache holding at most a fixed number of entries                                                                                              |
| `NewCache[K, V](size)`               | Creates a cache of at most `size` entries; a non-positive size is an error                                                                               |
| `NewWeightedCache[K, V](size, cost)` | Also bounds the total cost given to `PutWithCost`; a non-positive bound is an error                                                                      |
| `Cache.Get`                          | Value and `true` on a hit (marking the entry referenced), zero value and `false` otherwise                                                               |
| `Cache.Put`, `Cache.PutWithCost`     | Adds or updates an entry, evicting until the bounds hold; reports whether an entry was evicted. A value whose cost alone exceeds the bound is not stored |
| `Cache.Len`, `Cache.Cost`            | Number of entries and their total cost                                                                                                                   |
| `Cache.GetOrCompute`                 | Cached value, or computes it once per key through singleflight and caches it; errors are not cached                                                      |

## Usage

```go
cache, err := lru.NewCache[string, *Spec](size)
if err != nil {
    return nil, err
}

spec, err := cache.GetOrCompute(ctx, input, func(ctx context.Context) (*Spec, error) {
    return parse(ctx, input)
})
```

Callers cancel independently; a shared computation receives a context bounded by the singleflight default timeout (15 seconds).

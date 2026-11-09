// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lru_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/lru"
)

func TestNewCache_RejectsNonPositiveSize(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, -1} {
		_, err := lru.NewCache[string, int](size)
		require.Error(t, err, "size %d", size)
	}
}

func TestCache_PutGetLen(t *testing.T) {
	t.Parallel()

	c, err := lru.NewCache[string, int](10)
	require.NoError(t, err)

	_, ok := c.Get("a")
	require.False(t, ok)
	require.False(t, c.Put("a", 1))
	require.False(t, c.Put("b", 2))
	require.False(t, c.Put("a", 3), "updating a key evicts nothing")

	v, ok := c.Get("a")
	require.True(t, ok)
	require.Equal(t, 3, v)
	require.Equal(t, 2, c.Len())
}

func TestCache_EvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	c, err := lru.NewCache[string, int](2)
	require.NoError(t, err)
	c.Put("a", 1)
	c.Put("b", 2)
	_, _ = c.Get("a") // a is now more recent than b

	require.True(t, c.Put("c", 3), "a third entry evicts one")
	require.Equal(t, 2, c.Len())

	_, ok := c.Get("b")
	require.False(t, ok, "the least recently used entry is evicted")

	for _, k := range []string{"a", "c"} {
		_, ok := c.Get(k)
		require.True(t, ok, k)
	}
}

func TestCache_GetOrCompute(t *testing.T) {
	t.Parallel()

	c, err := lru.NewCache[string, string](10)
	require.NoError(t, err)

	v, err := c.GetOrCompute(t.Context(), "key", func(context.Context) (string, error) { return "computed", nil })
	require.NoError(t, err)
	require.Equal(t, "computed", v)

	cached, ok := c.Get("key")
	require.True(t, ok, "the computed value is cached")
	require.Equal(t, "computed", cached)

	v, err = c.GetOrCompute(t.Context(), "key", func(context.Context) (string, error) {
		t.Error("compute must not run on a hit")

		return "", nil
	})
	require.NoError(t, err)
	require.Equal(t, "computed", v)
}

func TestCache_GetOrComputeErrorIsNotCached(t *testing.T) {
	t.Parallel()

	c, err := lru.NewCache[string, string](10)
	require.NoError(t, err)

	wantErr := errors.New("fail")
	_, err = c.GetOrCompute(t.Context(), "key", func(context.Context) (string, error) { return "", wantErr })
	require.ErrorIs(t, err, wantErr)

	_, ok := c.Get("key")
	require.False(t, ok, "a failed computation is not cached")
	require.Zero(t, c.Len())
}

func TestCache_GetOrComputeDeduplicatesConcurrentMisses(t *testing.T) {
	t.Parallel()

	c, err := lru.NewCache[string, int](10)
	require.NoError(t, err)

	var (
		calls   atomic.Int32
		release = make(chan struct{})
		started sync.WaitGroup
		done    sync.WaitGroup
	)

	const callers = 16

	started.Add(callers)
	done.Add(callers)

	for range callers {
		go func() {
			defer done.Done()

			started.Done()

			v, err := c.GetOrCompute(t.Context(), "key", func(context.Context) (int, error) {
				calls.Add(1)
				<-release

				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("GetOrCompute = %d, %v", v, err)
			}
		}()
	}

	started.Wait()
	close(release)
	done.Wait()

	require.LessOrEqual(t, calls.Load(), int32(callers))
	require.GreaterOrEqual(t, calls.Load(), int32(1))

	v, ok := c.Get("key")
	require.True(t, ok)
	require.Equal(t, 42, v)
}

func TestCache_BoundHoldsAcrossShards(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, 2, 33, 1000, 5000} {
		c, err := lru.NewCache[int, int](size)
		require.NoError(t, err)

		for i := range 3 * size {
			c.Put(i, i)
			require.LessOrEqual(t, c.Len(), size, "size %d after %d puts", size, i+1)
		}

		require.Equal(t, size, c.Len(), "size %d", size)

		hits := 0

		for i := range 3 * size {
			if v, ok := c.Get(i); ok {
				require.Equal(t, i, v)

				hits++
			}
		}

		require.Equal(t, size, hits, "every retained entry is reachable")
	}
}

func TestCache_ReferencedEntriesSurviveEviction(t *testing.T) {
	t.Parallel()

	const size = 64

	c, err := lru.NewCache[int, int](size)
	require.NoError(t, err)

	for i := range size {
		c.Put(i, i)
	}

	// Reference the even keys; the next size/2 inserts evict only odd ones.
	for i := 0; i < size; i += 2 {
		_, _ = c.Get(i)
	}

	for i := range size / 2 {
		require.True(t, c.Put(size+i, i), "a full cache evicts")
	}

	for i := 0; i < size; i += 2 {
		_, ok := c.Get(i)
		require.True(t, ok, "referenced key %d survives", i)
	}
}

func TestCache_UpdateReplacesValueAndCost(t *testing.T) {
	t.Parallel()

	c, err := lru.NewWeightedCache[string, string](10, 100)
	require.NoError(t, err)

	require.False(t, c.PutWithCost("a", "1", 40))
	require.False(t, c.PutWithCost("a", "2", 60))
	require.Equal(t, 60, c.Cost())
	require.Equal(t, 1, c.Len())

	v, ok := c.Get("a")
	require.True(t, ok)
	require.Equal(t, "2", v)
}

func TestWeightedCache_CostBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		puts      []int // costs of keys 0, 1, 2, …
		wantLen   int
		wantCost  int
		wantFirst bool // whether key 0 is still cached
	}{
		{name: "fits", puts: []int{30, 30, 30}, wantLen: 3, wantCost: 90, wantFirst: true},
		{name: "evicts until it fits", puts: []int{40, 40, 40}, wantLen: 2, wantCost: 80},
		{name: "one large entry flushes the rest", puts: []int{30, 30, 100}, wantLen: 1, wantCost: 100},
		{name: "an entry above the bound is not stored", puts: []int{30, 101}, wantLen: 1, wantCost: 30, wantFirst: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := lru.NewWeightedCache[int, int](10, 100)
			require.NoError(t, err)

			for i, cost := range tt.puts {
				c.PutWithCost(i, i, cost)
			}

			require.Equal(t, tt.wantLen, c.Len())
			require.Equal(t, tt.wantCost, c.Cost())

			_, ok := c.Get(0)
			require.Equal(t, tt.wantFirst, ok)
		})
	}
}

func TestWeightedCache_OversizedUpdateDropsOldValue(t *testing.T) {
	t.Parallel()

	c, err := lru.NewWeightedCache[string, int](10, 100)
	require.NoError(t, err)

	c.PutWithCost("a", 1, 10)
	c.PutWithCost("a", 2, 200)

	_, ok := c.Get("a")
	require.False(t, ok)
	require.Zero(t, c.Cost())
}

func TestNewWeightedCache_RejectsNonPositiveBounds(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ size, maxCost int }{{0, 10}, {10, 0}, {10, -1}} {
		_, err := lru.NewWeightedCache[string, int](tt.size, tt.maxCost)
		require.Error(t, err, "size %d, maxCost %d", tt.size, tt.maxCost)
	}
}

func TestCache_ConcurrentReadersAndWriters(t *testing.T) {
	t.Parallel()

	const size = 256

	c, err := lru.NewWeightedCache[int, int](size, 10*size)
	require.NoError(t, err)

	var wg sync.WaitGroup

	for w := range 8 {
		wg.Go(func() {
			for i := range 2000 {
				k := (w*7919 + i) % (4 * size)
				if v, ok := c.Get(k); ok && v != k {
					t.Errorf("Get(%d) = %d", k, v)
					return
				}

				if i%3 == 0 {
					c.PutWithCost(k, k, 1+k%20)
				}
			}
		})
	}

	wg.Wait()

	require.LessOrEqual(t, c.Len(), size)
	require.LessOrEqual(t, c.Cost(), 10*size)
}

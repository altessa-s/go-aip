// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	filter "github.com/altessa-s/go-aip/filter"
)

// wideIn returns a distinct `a in [i,1,1,...]` filter of about size bytes: the shape of SEC-6, whose AST holds about 25
// times the bytes of its source.
func wideIn(i, size int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "a in [%d", i)

	for b.Len() < size-2 {
		b.WriteString(",1")
	}

	b.WriteString("]")

	return b.String()
}

// TestCache_ByteBound reproduces SEC-6: distinct maximal filters must not pin more than the byte budget of ASTs.
func TestCache_ByteBound(t *testing.T) {
	t.Parallel()

	const budget, count = 1 << 20, 40

	p, err := New(WithCacheBytes(budget))
	require.NoError(t, err)

	for i := range count {
		_, err := p.ParseShared(t.Context(), wideIn(i, 1024))
		require.NoError(t, err)

		_, bytes, _ := p.cache.stats()
		require.LessOrEqual(t, bytes, budget)
	}

	entries, bytes, _ := p.cache.stats()
	require.Positive(t, entries)
	require.Less(t, entries, count, "the byte budget, not the entry count, must bound the cache")
	require.Greater(t, bytes, budget/2, "the cache should still use its budget")
}

func TestCache_EntryBound(t *testing.T) {
	t.Parallel()

	p, err := New(WithCacheSize(3))
	require.NoError(t, err)

	for i := range 10 {
		_, err := p.ParseShared(t.Context(), fmt.Sprintf("a == %d", i))
		require.NoError(t, err)
	}

	entries, _, _ := p.cache.stats()
	require.Equal(t, 3, entries)

	// The most recent entries survive: a hit returns the shared cached tree.
	first, err := p.ParseShared(t.Context(), "a == 9")
	require.NoError(t, err)
	second, err := p.ParseShared(t.Context(), "a == 9")
	require.NoError(t, err)
	require.Same(t, first, second)
}

// TestCache_OversizedEntryNotCached keeps one huge AST from flushing the cache: above a sixteenth of the budget it is
// returned but not stored.
func TestCache_OversizedEntryNotCached(t *testing.T) {
	t.Parallel()

	p, err := New(WithCacheBytes(16 << 10))
	require.NoError(t, err)

	_, err = p.ParseShared(t.Context(), `name == "x"`)
	require.NoError(t, err)

	expr := wideIn(0, 2048)
	first, err := p.ParseShared(t.Context(), expr)
	require.NoError(t, err)
	second, err := p.ParseShared(t.Context(), expr)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, first, second)

	entries, _, _ := p.cache.stats()
	require.Equal(t, 1, entries, "the small entry stays, the oversized one is not cached")
}

// TestCache_NegativeCaching pins PERF-12 (a): a deterministic parse failure is remembered, so repeating an invalid filter
// returns the same error without parsing it again.
func TestCache_NegativeCaching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		wantErr error
	}{
		{name: "syntax error", expr: "((((", wantErr: filter.ErrParseFailed},
		{name: "escaped field", expr: "a.`b c` == 1", wantErr: filter.ErrInvalidExpression},
		{name: "root-scoped field", expr: ".a == 1", wantErr: filter.ErrInvalidExpression},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New()
			require.NoError(t, err)

			_, first := p.Parse(t.Context(), tt.expr)
			require.ErrorIs(t, first, tt.wantErr)

			_, second := p.ParseShared(t.Context(), tt.expr)
			require.ErrorIs(t, second, tt.wantErr)
			require.Equal(t, first, second)

			entries, _, failures := p.cache.stats()
			require.Zero(t, entries, "a rejected filter must not enter the AST cache")
			require.Equal(t, 1, failures)
		})
	}
}

func TestCache_NegativeCacheBound(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	for i := range failureCacheSize + 50 {
		_, err := p.Parse(t.Context(), fmt.Sprintf("a == %d ((", i))
		require.ErrorIs(t, err, filter.ErrParseFailed)
	}

	_, _, failures := p.cache.stats()
	require.Equal(t, failureCacheSize, failures)
}

// TestCache_ParseOwnership keeps the Parse and ParseShared contracts: Parse returns a caller-owned copy on a hit, ParseShared
// the shared cached tree.
func TestCache_ParseOwnership(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	const expr = `name == "John"`

	shared, err := p.ParseShared(t.Context(), expr)
	require.NoError(t, err)

	owned, err := p.Parse(t.Context(), expr)
	require.NoError(t, err)
	require.Equal(t, shared, owned)
	require.NotSame(t, shared, owned)

	owned.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name = "mutated" //nolint:forcetypeassert // shape asserted by the parse

	again, err := p.ParseShared(t.Context(), expr)
	require.NoError(t, err)
	require.Same(t, shared, again)
	require.Equal(t, "name", again.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name) //nolint:forcetypeassert // shape asserted by the parse
}

func TestCache_Concurrent(t *testing.T) {
	t.Parallel()

	p, err := New(WithCacheSize(8), WithCacheBytes(64<<10))
	require.NoError(t, err)

	var wg sync.WaitGroup

	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				k := (g + i) % 20
				switch k % 4 {
				case 0:
					_, err := p.Parse(t.Context(), "((( "+fmt.Sprint(k))
					require.ErrorIs(t, err, filter.ErrParseFailed)
				case 1:
					_, err := p.ParseShared(t.Context(), wideIn(k, 512))
					require.NoError(t, err)
				default:
					_, err := p.Parse(t.Context(), fmt.Sprintf("a == %d", k))
					require.NoError(t, err)
				}
			}
		})
	}

	wg.Wait()

	entries, bytes, failures := p.cache.stats()
	require.LessOrEqual(t, entries, 8)
	require.LessOrEqual(t, bytes, 64<<10)
	require.Positive(t, failures)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/internal/regexcache"
)

// TestCompile pins that Compile returns the same compiled pattern on a second call and does not cache a pattern that fails.
func TestCompile(t *testing.T) {
	t.Parallel()

	cache, err := regexcache.New(256)
	require.NoError(t, err)
	cache.ResetStats()

	first, err := cache.Compile(`^a+b$`)
	require.NoError(t, err)
	second, err := cache.Compile(`^a+b$`)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.True(t, first.MatchString("aab"))

	_, err = cache.Compile(`(`)
	require.Error(t, err)
	_, err = cache.Compile(`(`)
	require.Error(t, err)

	hits, misses, size := cache.Stats()
	require.Equal(t, uint64(1), hits)
	require.Equal(t, uint64(3), misses)
	require.Equal(t, 1, size)
}

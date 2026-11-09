// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func TestRegexCacheStats(t *testing.T) {
	t.Parallel()
	ev := mustEvaluator(t)
	ev.ResetRegexCacheStats()

	stats := ev.RegexCacheStatsSnapshot()
	require.Zero(t, stats.Hits, "fresh stats Hits not zero")
	require.Zero(t, stats.Misses, "fresh stats Misses not zero")
	require.Equal(t, float64(0), stats.HitRate())
	require.Equal(t, uint64(0), stats.TotalLookups())
}

func TestRegexCacheStats_ViaEvaluator(t *testing.T) {
	t.Parallel()
	ev := mustEvaluator(t)
	ev.ResetRegexCacheStats()

	// Evaluate a filter with matches() to trigger regex compilation.
	p := newTestParser(t)
	node, err := p.Parse(t.Context(), `name.matches("^test.*$")`)
	require.NoError(t, err, "Parse")

	data := map[string]any{"name": "test123"}
	_, err = ev.Evaluate(node, data)
	require.NoError(t, err, "Evaluate")

	stats := ev.RegexCacheStatsSnapshot()
	// First call is a miss (compilation required).
	require.GreaterOrEqual(t, stats.Misses, uint64(1), "Misses after first matches()")
	initialSize := stats.Size

	// Second evaluation with the same pattern should be a hit.
	_, err = ev.Evaluate(node, data)
	require.NoError(t, err, "Evaluate")

	stats = ev.RegexCacheStatsSnapshot()
	require.GreaterOrEqual(t, stats.Hits, uint64(1), "Hits after second matches()")
	require.Greater(t, stats.HitRate(), float64(0), "HitRate()")
	require.GreaterOrEqual(t, stats.Size, initialSize, "Size")
}

func TestRegexCacheStats_Reset(t *testing.T) {
	t.Parallel()
	ev := mustEvaluator(t)
	ev.ResetRegexCacheStats()

	stats := ev.RegexCacheStatsSnapshot()
	require.Zero(t, stats.Hits, "stats Hits not zero after reset")
	require.Zero(t, stats.Misses, "stats Misses not zero after reset")
}

func TestRegexCacheStats_HitRate(t *testing.T) {
	s := filtereval.RegexCacheStats{Hits: 8, Misses: 2}
	require.Equal(t, 0.8, s.HitRate())
	require.Equal(t, uint64(10), s.TotalLookups())
}

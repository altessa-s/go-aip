// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/filter/regexanchor"
)

// TestFilterTranslate_MatchesAnchorCached pins the memoized end-of-text
// rewrite: repeated and concurrent translations of a matches() pattern, by
// one translator or several, render what the uncached rewrite renders.
func TestFilterTranslate_MatchesAnchorCached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expr     string
		pattern  string
		wantJSON string
	}{
		{"end of text", `name.matches("^A.*e$")`, `^A.*e$`, `{"name":{"$regex":"^A.*e\\z"}}`},
		{"multi-line keeps dollar", `name.matches("(?m)^a$")`, `(?m)^a$`, `{"name":{"$regex":"(?m)^a$"}}`},
		{"quoted dollar", `name.matches("\\Qa$\\E$")`, `\Qa$\E$`, `{"name":{"$regex":"\\Qa$\\E\\z"}}`},
		{"no dollar", `name.matches("^ab")`, `^ab`, `{"name":{"$regex":"^ab"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want, err := regexanchor.EndOfText(tt.pattern)
			require.NoError(t, err)

			got, err := endOfText(tt.pattern)
			require.NoError(t, err)
			require.Equal(t, want, got)

			node := filtertest.MustParseFilter(t, tt.expr)

			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					trans := mustTranslator(t)
					for range 3 {
						out, err := trans.Translate(node)
						require.NoError(t, err)
						require.JSONEq(t, tt.wantJSON, bsonToJSON(out))
					}
				})
			}

			wg.Wait()
		})
	}
}

// TestPatternCache_Bound pins the bound of the end-of-text memo: a full cache
// is emptied before it admits a new key, an update of a cached key keeps the
// other entries, and concurrent use stays within the bound.
func TestPatternCache_Bound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		puts      []string
		wantCount int
		wantKeys  []string
		wantGone  []string
	}{
		{name: "below the bound", puts: []string{"a", "b"}, wantCount: 2, wantKeys: []string{"a", "b"}},
		{name: "update at the bound", puts: []string{"a", "b", "c", "a"}, wantCount: 3, wantKeys: []string{"a", "b", "c"}},
		{name: "new key resets", puts: []string{"a", "b", "c", "d"}, wantCount: 1, wantKeys: []string{"d"}, wantGone: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newPatternCache(3)
			for _, k := range tt.puts {
				c.put(k, k+"!")
			}

			require.Equal(t, tt.wantCount, c.count())

			for _, k := range tt.wantKeys {
				v, ok := c.get(k)
				require.True(t, ok, k)
				require.Equal(t, k+"!", v)
			}

			for _, k := range tt.wantGone {
				_, ok := c.get(k)
				require.False(t, ok, k)
			}
		})
	}

	t.Run("concurrent", func(t *testing.T) {
		t.Parallel()

		c := newPatternCache(8)

		var wg sync.WaitGroup
		for w := range 4 {
			wg.Go(func() {
				for i := range 100 {
					k := string(rune('a' + (w*100+i)%26))
					c.put(k, k)

					if v, ok := c.get(k); ok {
						require.Equal(t, k, v)
					}
				}
			})
		}

		wg.Wait()
		require.LessOrEqual(t, c.count(), 8)
	})
}

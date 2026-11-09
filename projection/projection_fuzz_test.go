// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/projection"
)

// FuzzParse checks the invariants of every accepted expression: the paths
// are sorted, unique, non-empty, and made of identifier segments only, and
// re-parsing the joined result yields the same Spec.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{"", "*", "a", "a,b", "a.b, c", "a,,b", "items.*.x", "items.0", "`k`", "$x", "a..b", " a , a "} {
		f.Add(seed)
	}

	p, err := projection.NewParser(projection.WithNoCache(true))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, in string) {
		spec, err := p.Parse(t.Context(), in)
		if err != nil {
			return
		}

		require.True(t, slices.IsSorted(spec.Paths))
		require.Equal(t, len(spec.Paths), len(slices.Compact(slices.Clone(spec.Paths))))

		for _, path := range spec.Paths {
			require.NotEmpty(t, path)
			require.False(t, strings.ContainsAny(path, " ,*`$"), path)
		}

		input := strings.Join(spec.Paths, ",")
		if spec.Mode == projection.ModeAll {
			input = "*"
		}

		again, err := p.Parse(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, spec, again)
	})
}

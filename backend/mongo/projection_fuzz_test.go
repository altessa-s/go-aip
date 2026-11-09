// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/projection"
)

// FuzzProjectionTranslate feeds parsed client input through a policy with mapping,
// a deny-list and a required field, and checks the MongoDB invariants of
// every accepted projection: no two keys collide, no key carries an
// operator, values are all 1 or all 0 (except _id), and no key overlaps a
// denied path.
func FuzzProjectionTranslate(f *testing.F) {
	for _, seed := range []string{"", "a", "a,a.b", "secret", "secret.x", "addr.city,address", "x.y,x"} {
		f.Add(seed)
	}

	parser, err := projection.NewParser(projection.WithNoCache(true))
	require.NoError(f, err)
	tr := newProjectionTranslator(f,
		projection.WithFieldMapping(map[string]string{"address": "addr"}),
		projection.WithDeniedFields("secret"),
		projection.WithRequiredFields("meta.version"),
	)

	f.Fuzz(func(t *testing.T, in string) {
		spec, err := parser.Parse(t.Context(), in)
		if err != nil {
			return
		}

		doc, err := tr.Translate(spec)
		if err != nil {
			return
		}

		var ones, zeros int

		for k, v := range doc {
			require.False(t, strings.HasPrefix(k, "$"), k)
			require.False(t, fieldpolicy.Overlaps(k, "secret") && v != int32(0), k)

			if k != "_id" {
				if v == int32(1) {
					ones++
				} else {
					zeros++
				}
			}

			for other := range doc {
				require.False(t, other != k && fieldpolicy.Overlaps(k, other), "%s collides with %s", k, other)
			}
		}

		require.False(t, ones > 0 && zeros > 0, "mixed inclusion and exclusion: %v", doc)
	})
}

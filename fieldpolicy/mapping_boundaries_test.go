// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
)

func TestExactSubtreeMappingReach(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"private", "private."} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			mapping := fieldpolicy.Mapping{}.WithExact(map[string]string{"credentials.": target})
			require.Equal(t, []string{"private"}, mapping.Subtree("credentials"))
			require.Equal(t, []string{"credentials.password", "private.password"}, mapping.Reach("credentials.password"))
			require.Equal(t, []string{"credentials.password.hash", "private.password.hash"}, mapping.Reach("credentials.password.hash"))
			require.Equal(t, []string{"credentials_other.password"}, mapping.Reach("credentials_other.password"))
		})
	}
}

func TestExactMovedSubtreeNormalizesTarget(t *testing.T) {
	t.Parallel()

	mapping := fieldpolicy.Mapping{}.WithExact(map[string]string{"profile.credentials.": "private."})
	require.ElementsMatch(t, []string{"profile", "private"}, mapping.Subtree("profile"))
	require.ElementsMatch(t, []string{"profile", "private"}, mapping.AppendSubtree(nil, "profile"))
}

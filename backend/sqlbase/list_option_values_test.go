// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParentKeyCanBeDisabled(t *testing.T) {
	t.Parallel()
	require.Empty(t, newListOptions(WithListParentKey("owner"), WithListParentKey("")).parentKey)
}

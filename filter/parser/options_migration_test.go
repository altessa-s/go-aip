// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFunctionOptionsCurrentBehavior(t *testing.T) {
	t.Parallel()

	o := newOptions(WithAllowedFunctions(" contains ", "matches", "matches"))
	require.Equal(t, map[string]struct{}{" contains ": {}, "matches": {}}, o.allowedFunctions)

	WithAllowedFunctions()(o)
	require.NotNil(t, o.allowedFunctions)
	require.Empty(t, o.allowedFunctions)
}

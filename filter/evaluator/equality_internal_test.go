// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// valuesEqual compares only the forms storedValue returns: a struct of any other type is opaque and equals nothing, without
// reflection — neither a value-level comparability check, which walks every element of an array, nor == on contents that may not
// be comparable (a slice in an interface field panics).
func TestValuesEqual_NestedNonComparable(t *testing.T) {
	t.Parallel()

	type box struct{ V any }

	a, b := box{V: []int{1}}, box{V: []int{1}}

	require.NotPanics(t, func() { require.False(t, valuesEqual(nil, a, b)) })
	require.False(t, valuesEqual(nil, box{V: 1}, box{V: 1}))
	require.True(t, valuesEqual(nil, nil, nil))
	require.False(t, valuesEqual(nil, nil, 1))
}

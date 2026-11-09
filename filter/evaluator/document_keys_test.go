// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/evaluator"
)

func TestDocumentKeys(t *testing.T) {
	t.Parallel()

	keys := evaluator.DocumentKeys{Budget: &units{left: 1}}
	require.True(t, keys.Take("a"), "a short key costs no unit")
	require.True(t, keys.Take(strings.Repeat("k", evaluator.BytesPerUnit)), "a unit per BytesPerUnit bytes")
	require.False(t, keys.Take(strings.Repeat("k", evaluator.BytesPerUnit)), "the budget is spent")

	large := evaluator.DocumentKeys{Budget: &units{left: 1 << 30}}
	require.False(t, large.Take(strings.Repeat("k", evaluator.MaxDocumentBytes+1)), "beyond a document's size")
}

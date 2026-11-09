// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestidtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/requestid"
	"github.com/altessa-s/go-aip/requestid/requestidtest"
)

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	requestidtest.Run(t, func(*testing.T) requestid.Store { return requestid.NewMemoryStore() })
}

func TestKey(t *testing.T) {
	t.Parallel()

	k := requestidtest.Key("a")
	require.Len(t, k, 64)
	require.Equal(t, k, requestidtest.Key("a"))
	require.NotEqual(t, k, requestidtest.Key("b"))
}

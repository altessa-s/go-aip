// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listingtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing/listingtest"
)

func TestFixtureIsolation(t *testing.T) {
	t.Parallel()

	fields := listingtest.Fields()
	fields[2].Enum["KIND_A"] = "changed"
	fields[0].Name = "changed"
	require.Equal(t, "id", listingtest.Fields()[0].Name)
	require.Equal(t, "a", listingtest.Fields()[2].Enum["KIND_A"])

	paths := listingtest.FieldPaths()
	paths["id"] = "changed"

	require.Equal(t, "_id", listingtest.FieldPaths()["id"])

	patterns := listingtest.Patterns()
	patterns[0] = "changed"

	require.Equal(t, "createTime desc", listingtest.Patterns()[0])

	funcs := listingtest.Functions()
	delete(funcs, "search")
	require.Contains(t, listingtest.Functions(), "search")
}

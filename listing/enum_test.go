// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestEnumMetadataIsIsolated(t *testing.T) {
	t.Parallel()

	values := map[string]any{"ZERO": 0, "ONE": 1}
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "state", Kind: filter.FieldKindInt, Optional: true, Enum: values, EnumZero: "ZERO"}}, []string{"id"}, nil)
	require.NoError(t, err)

	values["ZERO"] = 99
	first, ok := schema.Field("state")
	require.True(t, ok)
	require.Equal(t, int64(0), first.Enum["ZERO"])
	first.Enum["ZERO"] = 100

	for range 4 {
		t.Run("snapshot", func(t *testing.T) {
			t.Parallel()

			f, _ := schema.Field("state")
			require.Equal(t, int64(0), f.Enum["ZERO"])
			f.Enum["ZERO"] = 200
		})
	}

	_, err = schema.Compile(t.Context(), listing.Request{Filter: `state == "ZERO"`})
	require.NoError(t, err)
}

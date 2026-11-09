// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestArrayElementBudget(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		option memory.ListEngineOption
		want   error
	}{
		{"data", memory.WithListMaxDataBudget(100), filter.ErrDataLimitExceeded},
		{"request", memory.WithListMaxWork(100), memory.ErrWorkBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cyclic any

			cyclic = &cyclic
			schema := listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}, []string{"name"}, nil)
			engine, err := memory.NewListEngine(nil, schema, func(int) string { return "a" }, func(int) map[string]any { return map[string]any{"name": "a", "tags": []any{cyclic}} }, tc.option)
			require.NoError(t, err)
			_, err = engine.List(t.Context(), "items", listing.Request{Filter: `tags == "a"`}, []int{1})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

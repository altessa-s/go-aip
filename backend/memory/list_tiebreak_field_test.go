// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestIndependentIdentity(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindInt}}, []string{"id"}, nil)
	engine, err := memory.NewListEngine(nil, schema, func(i int) int { return -i }, func(i int) map[string]any { return map[string]any{"id": i} })
	require.NoError(t, err)
	page, err := engine.List(t.Context(), "items", listing.Request{}, []int{2, 1})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, page.Items)
}

func TestTiebreakFieldValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field listing.Field
	}{
		{"missing", listing.Field{Name: "id", Kind: filter.FieldKindString}},
		{"array", listing.Field{Name: "uid", Kind: filter.FieldKindString, Array: true}},
		{"optional", listing.Field{Name: "uid", Kind: filter.FieldKindString, Optional: true}},
		{"presence", listing.Field{Name: "uid", Kind: filter.FieldKindString, Presence: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema := listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}, tc.field}, []string{"name"}, nil)
			_, err := memory.NewListEngine(nil, schema, func(int) string { return "a" }, func(int) map[string]any { return nil }, memory.WithListTiebreakField("uid"))
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}
}

func TestTiebreakStoredValue(t *testing.T) {
	t.Parallel()

	for _, value := range []any{math.NaN(), math.Inf(1), "wrong type", nil} {
		schema := listing.MustSchema([]listing.Field{{Name: "uid", Kind: filter.FieldKindFloat}}, []string{"uid"}, nil)
		engine, err := memory.NewListEngine(nil, schema, func(int) string { return "a" }, func(int) map[string]any { return map[string]any{"uid": value} }, memory.WithListTiebreakField("uid"))
		require.NoError(t, err)
		_, err = engine.List(t.Context(), "items", listing.Request{}, []int{1})
		require.ErrorIs(t, err, pagetoken.ErrUnsortableValue, "%v", value)
	}
}

type customSortCodec struct{}

func (customSortCodec) ValidateLiteral(v any) error {
	if _, ok := v.(string); !ok {
		return filter.ErrFieldTypeMismatch
	}

	return nil
}

func TestCustomKindSort(t *testing.T) {
	t.Parallel()

	const kind filter.FieldKind = 100

	schema := listing.MustSchema([]listing.Field{{Name: "rank", Kind: kind}}, []string{"rank"}, listing.NewProfile(listing.WithSortFieldValidator(nil), listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: customSortCodec{}}))))
	engine, err := memory.NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"rank": v} })
	require.NoError(t, err)
	page, err := engine.List(t.Context(), "custom", listing.Request{}, []string{"b", "a"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, page.Items)
}

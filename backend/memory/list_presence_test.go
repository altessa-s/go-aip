// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

type presenceItem struct {
	ID       string
	Rating   *int64
	Priority int64
	Score    *string
	Active   *bool
}

// TestPresenceNullFilters checks the default evaluator's semantics for a null presence field: it equals null and differs from
// every value, is in no list and contains no string, while an ordering comparison or a bare boolean test on it is unknown.
func TestPresenceNullFilters(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "priority", Kind: filter.FieldKindInt},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
		{Name: "active", Kind: filter.FieldKindBool, Presence: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := memory.NewListEngine(nil, schema, func(x presenceItem) string { return x.ID }, func(x presenceItem) map[string]any {
		return map[string]any{"id": x.ID, "rating": x.Rating, "priority": x.Priority, "score": x.Score, "active": x.Active}
	})
	require.NoError(t, err)

	one, two, three, four := int64(1), int64(2), int64(3), int64(4)
	a, b := "apple", "berry"
	trueValue, falseValue := true, false
	items := []presenceItem{
		{ID: "absent", Priority: 3}, {ID: "one", Rating: &one, Priority: 3, Score: &a, Active: &trueValue},
		{ID: "two", Rating: &two, Priority: 3, Score: &b, Active: &falseValue},
		{ID: "three", Rating: &three, Priority: 4, Score: &b}, {ID: "four", Rating: &four, Priority: 3},
	}

	cases := []struct {
		filter string
		want   []string
	}{
		{`!(rating == 1 || priority == 4)`, []string{"absent", "four", "two"}},
		{`!!(rating > 1)`, []string{"four", "three", "two"}},
		{`!(rating in [1, 2])`, []string{"absent", "four", "three"}},
		{`!(rating > 1)`, []string{"one"}},
		{`!(rating == 1)`, []string{"absent", "four", "three", "two"}},
		{`rating == null`, []string{"absent"}},
		{`rating != null`, []string{"four", "one", "three", "two"}},
		{`rating != 1`, []string{"absent", "four", "three", "two"}},
		{`!(score.startsWith("a"))`, []string{"absent", "four", "three", "two"}},
		{`!active`, []string{"two"}},
		{`rating == 1 || priority == 4`, []string{"one", "three"}},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			t.Parallel()
			got, err := list.List(t.Context(), "widgets.List", listing.Request{Filter: tc.filter, PageSize: 20}, items)
			require.NoError(t, err)

			ids := make([]string, len(got.Items))
			for i := range got.Items {
				ids[i] = got.Items[i].ID
			}

			slices.Sort(ids)
			require.Equal(t, tc.want, ids)
		})
	}
}

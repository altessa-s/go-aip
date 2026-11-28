// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestNullablePresenceSort pages a sortable Presence field in every direction and page size: null first ascending and
// last descending, ties broken by id in the direction of the last key, as PostgreSQL orders the rows.
func TestNullablePresenceSort(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "priority", Kind: filter.FieldKindInt},
	}, []string{"id"}, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithSortableFields("id", "rating", "priority")))
	require.NoError(t, err)
	list, err := memory.NewListEngine(pagetokentest.Codec(t), schema, func(x presenceItem) string { return x.ID }, func(x presenceItem) map[string]any {
		return map[string]any{"id": x.ID, "rating": x.Rating, "priority": x.Priority}
	})
	require.NoError(t, err)

	var items []presenceItem

	for i := range 12 {
		item := presenceItem{ID: string(rune('a' + i)), Priority: int64(i % 2)}
		if i%3 != 0 {
			item.Rating = new(int64(i % 4))
		}

		items = append(items, item)
	}

	// rank orders a rating as PostgreSQL does: null below every value.
	rank := func(x presenceItem) int64 {
		if x.Rating == nil {
			return -1
		}

		return *x.Rating
	}

	for _, tc := range []struct {
		orderBy string
		less    func(a, b presenceItem) int
	}{
		{"rating", func(a, b presenceItem) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.ID, b.ID)) }},
		{"rating desc", func(a, b presenceItem) int { return cmp.Or(cmp.Compare(rank(b), rank(a)), cmp.Compare(b.ID, a.ID)) }},
		{"priority, rating desc", func(a, b presenceItem) int {
			return cmp.Or(cmp.Compare(a.Priority, b.Priority), cmp.Compare(rank(b), rank(a)), cmp.Compare(b.ID, a.ID))
		}},
		{"rating, priority desc", func(a, b presenceItem) int {
			return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(b.Priority, a.Priority), cmp.Compare(b.ID, a.ID))
		}},
	} {
		want := slices.Clone(items)
		slices.SortFunc(want, tc.less)

		wantIDs := make([]string, len(want))
		for i, x := range want {
			wantIDs[i] = x.ID
		}

		for _, size := range []int32{1, 2, 5, 12} {
			var got []string

			req := listing.Request{OrderBy: tc.orderBy, PageSize: size}
			for {
				page, err := list.List(t.Context(), "assets.List", req, items)
				require.NoError(t, err, "%s, page size %d", tc.orderBy, size)

				for _, x := range page.Items {
					got = append(got, x.ID)
				}

				if page.NextPageToken == "" {
					break
				}

				req.PageToken = page.NextPageToken
			}

			require.Equal(t, wantIDs, got, "%s, page size %d", tc.orderBy, size)
		}
	}

	// A required field without a value still fails the request.
	required, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt},
	}, []string{"rating"}, nil)
	require.NoError(t, err)
	strict, err := memory.NewListEngine(nil, required, func(x presenceItem) string { return x.ID }, func(x presenceItem) map[string]any {
		return map[string]any{"id": x.ID, "rating": x.Rating}
	})
	require.NoError(t, err)
	_, err = strict.List(t.Context(), "assets.List", listing.Request{PageSize: 20}, items)
	require.Error(t, err)
}

func TestNullablePresenceSortWithCustomComparator(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
	}, []string{"rating"}, listing.NewProfile(listing.WithSortableFields("rating")))
	l, err := memory.NewListEngine(pagetokentest.Codec(t), schema,
		func(x presenceItem) string { return x.ID },
		func(x presenceItem) map[string]any { return map[string]any{"rating": x.Rating} },
		memory.WithListPositionCodec(reverseCodec{}))
	require.NoError(t, err)

	items := []presenceItem{{ID: "low", Rating: new(int64(1))}, {ID: "null"}, {ID: "high", Rating: new(int64(2))}}

	req := listing.Request{PageSize: 1}
	for i, id := range []string{"null", "high", "low"} {
		page, err := l.List(t.Context(), "items", req, items)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, id, page.Items[0].ID)
		require.Equal(t, i < 2, page.NextPageToken != "")
		req.PageToken = page.NextPageToken
	}
}

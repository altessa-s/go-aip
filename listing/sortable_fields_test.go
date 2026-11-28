// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

var sortableDeclared = []listing.Field{
	{Name: "name", Kind: filter.FieldKindString},
	{Name: "owner", Kind: filter.FieldKindString, Presence: true},
	{Name: "rank", Kind: filter.FieldKindInt},
	{Name: "tags", Kind: filter.FieldKindString, Array: true},
	{Name: "blob", Kind: filter.FieldKindBytes},
}

// TestSortableFields lets requests combine exactly the named fields, presence ones included.
func TestSortableFields(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema(sortableDeclared, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true),
		listing.WithSortableFields("name", "owner")))
	require.NoError(t, err)
	require.Equal(t, []string{"name", "owner"}, s.SortFields())

	order, err := s.Order(t.Context(), "owner desc, name")
	require.NoError(t, err)
	require.Equal(t, "owner desc, name", order.String())

	_, err = s.Order(t.Context(), "rank")
	require.Equal(t, listing.FieldOrderBy, listingtest.FieldOf(err))

	q, err := s.Compile(t.Context(), listing.Request{OrderBy: "owner"})
	require.NoError(t, err)
	require.Equal(t, "owner", q.Order.String())
}

// TestSortableFieldsRejects refuses names that are undeclared or do not sort, and patterns over other fields.
func TestSortableFieldsRejects(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		patterns []string
		sortable []string
	}{
		"undeclared":      {[]string{"name"}, []string{"name", "missing"}},
		"array":           {[]string{"name"}, []string{"name", "tags"}},
		"bytes":           {[]string{"name"}, []string{"name", "blob"}},
		"pattern outside": {[]string{"rank"}, []string{"name"}},
	} {
		_, err := listing.NewSchema(sortableDeclared, c.patterns, listing.NewProfile(listing.WithUnrestrictedOrder(true),
			listing.WithSortableFields(c.sortable...)))
		require.ErrorIs(t, err, listing.ErrInvalidSchema, name)
	}
}

// TestSortFieldsWithoutOption reports the declared pattern keys, and under an unrestricted profile every field the
// default validator accepts.
func TestSortFieldsWithoutOption(t *testing.T) {
	t.Parallel()

	s := listing.MustSchema(sortableDeclared, []string{"name, rank"}, nil)
	require.Equal(t, []string{"name", "rank"}, s.SortFields())

	s = listing.MustSchema(sortableDeclared, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true)))
	require.Equal(t, []string{"name", "rank"}, s.SortFields())
}

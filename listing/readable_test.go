// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// hiddenFields declares a hidden scalar field next to a readable one.
var hiddenFields = []listing.Field{
	{Name: "name", Kind: filter.FieldKindString},
	{Name: "score", Kind: filter.FieldKindInt, Hidden: true},
}

// TestHiddenSortFieldRejected checks a pattern over a hidden field is refused
// at start-up by default: a page token carries the sort values of the last row
// of a page, which the client would read although the field is hidden.
func TestHiddenSortFieldRejected(t *testing.T) {
	t.Parallel()

	for _, patterns := range [][]string{{"score"}, {"name", "name desc, score desc"}} {
		_, err := listing.NewSchema(hiddenFields, patterns, nil)
		require.ErrorIs(t, err, listing.ErrUnreadableSortField)
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
		require.Contains(t, err.Error(), `"score"`)
	}
}

// TestHiddenFieldFilterable checks a hidden field stays filterable when no
// pattern sorts on it.
func TestHiddenFieldFilterable(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema(hiddenFields, []string{"name"}, nil)
	require.NoError(t, err)
	_, err = s.Compile(t.Context(), listing.Request{Filter: "score > 3"})
	require.NoError(t, err)

	f, ok := s.Field("score")
	require.True(t, ok)
	require.True(t, f.Hidden)
}

// TestUnreadableSortAllowed checks the opt-out: a schema that deliberately
// sorts on a hidden field (with encrypted page tokens, say) is accepted.
func TestUnreadableSortAllowed(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema(hiddenFields, []string{"score"}, listing.NewProfile(listing.WithUnreadableSortAllowed(true)))
	require.NoError(t, err)
	q, err := s.Compile(t.Context(), listing.Request{ReadMask: []string{"name"}})
	require.NoError(t, err)
	require.Equal(t, "score", q.Order.String())
}

// TestReadMaskCoversOrder checks a request whose read mask omits a key of its
// order — requested or default — is a field error on orderBy, and that a mask
// naming every key, a parent path or the wildcard is served.
func TestReadMaskCoversOrder(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)

	tests := []struct {
		name    string
		orderBy string
		mask    []string
		wantErr bool
	}{
		{name: "no mask reads everything", orderBy: "kind, priority desc"},
		{name: "mask covers every key", orderBy: "kind, priority desc", mask: []string{"priority", "kind", "name"}},
		{name: "wildcard", orderBy: "name", mask: []string{"*"}},
		{name: "mask omits a key", orderBy: "kind, priority desc", mask: []string{"kind"}, wantErr: true},
		{name: "mask omits the default order", mask: []string{"name"}, wantErr: true},
		{name: "mask covers the default order", mask: []string{"createTime"}},
		{name: "prefix of a name is not its parent", orderBy: "name", mask: []string{"nam"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := s.Compile(t.Context(), listing.Request{OrderBy: tc.orderBy, ReadMask: tc.mask})
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, listing.ErrUnreadableSortField)
			require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
			require.Equal(t, listing.FieldOrderBy, fieldOf(err))
		})
	}
}

// TestReadMaskParentPath checks a mask path covers the fields below it.
func TestReadMaskParentPath(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "meta.createTime", Kind: filter.FieldKindTimestamp}}, []string{"meta.createTime"}, nil)
	require.NoError(t, err)
	_, err = s.Compile(t.Context(), listing.Request{ReadMask: []string{"meta"}})
	require.NoError(t, err)
	_, err = s.Compile(t.Context(), listing.Request{ReadMask: []string{"meta.updateTime"}})
	require.ErrorIs(t, err, listing.ErrUnreadableSortField)
}

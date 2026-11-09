// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

func TestMustNewListEngineReturnsTheEngine(t *testing.T) {
	t.Parallel()

	got := memory.MustNewListEngine(nil, listingtest.NewSchema(t), listingtest.ID, listingtest.Values)
	require.NotNil(t, got)

	page, err := got.List(t.Context(), "widgets", listing.Request{OrderBy: "id"}, []listingtest.Widget{{ID: "b"}, {ID: "a"}})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, widgetIDs(page.Items))
}

// TestMustNewListEnginePanics checks that MustNewListEngine panics with an
// error that names it and wraps the NewListEngine error.
func TestMustNewListEnginePanics(t *testing.T) {
	t.Parallel()

	_, want := memory.NewListEngine(nil, nil, listingtest.ID, listingtest.Values)
	require.ErrorIs(t, want, listing.ErrInvalidSchema)

	var recovered any

	func() {
		defer func() { recovered = recover() }()

		memory.MustNewListEngine(nil, nil, listingtest.ID, listingtest.Values)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "panic value %T is not an error", recovered)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Equal(t, "memory.MustNewListEngine: "+want.Error(), err.Error())
}

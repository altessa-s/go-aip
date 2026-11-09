// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

func TestMustNewListEngineReturnsTheEngine(t *testing.T) {
	t.Parallel()

	s := listingtest.NewSchema(t)
	got := mongo.MustNewListEngine[listingtest.Widget](nil, nil, s, widgetOptions()...)
	require.NotNil(t, got)
	want := newWidgets(t, s)
	require.Len(t, got.Indexes(), len(want.Indexes()))

	for _, o := range s.Orders() {
		require.Equal(t, want.Sort(o), got.Sort(o), o.String())
	}
}

// TestMustNewListEnginePanics checks that MustNewListEngine panics with an
// error that names it and wraps the NewListEngine error.
func TestMustNewListEnginePanics(t *testing.T) {
	t.Parallel()

	_, want := mongo.NewListEngine[listingtest.Widget](nil, nil, nil)
	require.ErrorIs(t, want, listing.ErrInvalidSchema)

	var recovered any

	func() {
		defer func() { recovered = recover() }()

		mongo.MustNewListEngine[listingtest.Widget](nil, nil, nil)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "panic value %T is not an error", recovered)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Equal(t, "mongo.MustNewListEngine: "+want.Error(), err.Error())
}

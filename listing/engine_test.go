// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/page"
)

type sampleEngine struct{}

func (sampleEngine) List(_ context.Context, subject string, req listing.Request, scope int) (page.Page[string], error) {
	return page.Page[string]{Items: []string{subject, req.Parent}, NextPageToken: string(rune(scope))}, nil
}

func TestBindEngine(t *testing.T) {
	t.Parallel()

	lister := listing.Bind[string](sampleEngine{}, "widgets", 42)
	got, err := lister.List(t.Context(), listing.Request{Parent: "tenant"})
	require.NoError(t, err)
	require.Equal(t, []string{"widgets", "tenant"}, got.Items)
	require.Equal(t, "*", got.NextPageToken)
}

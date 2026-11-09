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

func TestFieldNameValidationPolicy(t *testing.T) {
	t.Parallel()

	fields := []listing.Field{{Name: "custom-key", Kind: filter.FieldKindString}}
	_, err := listing.NewSchema(fields, nil, listing.NewProfile(listing.WithEmptyOrderAllowed(true)))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	_, err = listing.NewSchema(fields, nil, listing.NewProfile(listing.WithEmptyOrderAllowed(true), listing.WithFieldNameValidator(func(name string) error { require.Equal(t, "custom-key", name); return nil })))
	require.NoError(t, err)
}

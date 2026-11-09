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

func TestNewValidatesUnrestrictedDefaultOrder(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, nil,
		listing.NewProfile(listing.WithEmptyOrderAllowed(true), listing.WithUnrestrictedOrder(true), listing.WithDefaultOrderBy("id, name")))
	require.NoError(t, err)
	_, err = memory.NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v, "name": v} }, memory.WithListTiebreakField("id"))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

// TestListRejectsFoldedContains checks that the MongoDB engine, which cannot fold contains, refuses a schema that selects fields
// for it. The test lives in this module, not in the backend module: that module also builds against root releases that predate the
// option.
func TestListRejectsFoldedContains(t *testing.T) {
	t.Parallel()

	schema := listingtest.NewSchema(t, listing.WithCaseInsensitiveContainsFields("name"))

	_, err := mongo.NewListEngine[listingtest.Widget](nil, nil, schema)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

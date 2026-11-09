// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// TestOrderingAttributes: MongoDB sorts cannot express null placement or a
// collation per key (the MongoDB half of the orderby package's
// TestOrderingAttributes).
func TestOrderingAttributes(t *testing.T) {
	t.Parallel()

	spec := orderby.Spec{Keys: []orderby.Key{{Name: "name", Nulls: orderby.NullsLast, Collation: "C"}}}
	m, err := mongo.NewOrderTranslator(orderbytest.MustPolicy(t))
	require.NoError(t, err)

	_, err = m.Translate(spec)
	require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestServerTiebreakerPolicy(t *testing.T) {
	t.Parallel()

	c, err := orderby.NewTranslatorContext(orderbytest.MustPolicy(t, orderby.WithAllowedFields("name", "uid")),
		orderby.WithFieldMapping(map[string]string{"uid": "_id"}), orderby.WithTiebreaker(orderby.Key{Name: "_id"}))
	require.NoError(t, err)
	s, err := c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "name"}}})
	require.NoError(t, err)
	require.Equal(t, []orderby.Key{{Name: "name"}, {Name: "_id"}}, s.Keys)
	s, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "uid", Direction: orderby.DirectionDescending}}})
	require.NoError(t, err)
	require.Equal(t, []orderby.Key{{Name: "_id", Direction: orderby.DirectionDescending}}, s.Keys)
}

// TestTiebreakerAgainstDenials rejects, at construction, a tie-breaker that
// overlaps a denied storage path or a path a denied API field maps onto.
func TestTiebreakerAgainstDenials(t *testing.T) {
	t.Parallel()

	open := orderbytest.MustPolicy(t)
	_, err := orderby.NewTranslatorContext(open, orderby.WithDeniedStorageFields("secret"),
		orderby.WithTiebreaker(orderby.Key{Name: "secret.part"}))
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	denied := orderbytest.MustPolicy(t, orderby.WithDeniedFields("hash"))
	_, err = orderby.NewTranslatorContext(denied, orderby.WithFieldMapping(map[string]string{"hash": "pw"}),
		orderby.WithTiebreaker(orderby.Key{Name: "pw"}))
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	for _, k := range []orderby.Key{{}, {Name: "a", Direction: 9}, {Name: "a", Nulls: 9}} {
		_, err = orderby.NewTranslatorContext(open, orderby.WithTiebreaker(k))
		require.Error(t, err)
	}
}

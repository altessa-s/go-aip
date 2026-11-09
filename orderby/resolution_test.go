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

type keyResolver struct{}

func (keyResolver) ResolveKey(k orderby.Key) (orderby.Key, error) { k.Name = "stored"; return k, nil }

func TestResolveStorageKeys(t *testing.T) {
	t.Parallel()

	open := orderbytest.MustPolicy(t)
	c, err := orderby.NewTranslatorContext(open, orderby.WithFieldMapping(map[string]string{"a": "x", "b": "x"}))
	require.NoError(t, err)
	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "a"}, {Name: "b"}}})
	require.ErrorIs(t, err, orderby.ErrDuplicateKey)

	c, err = orderby.NewTranslatorContext(orderbytest.MustPolicy(t, orderby.WithDefaultOrder(orderby.Key{Name: "a"})),
		orderby.WithKeyResolver(keyResolver{}))
	require.NoError(t, err)
	got, err := c.Resolve(orderby.Spec{})
	require.NoError(t, err)
	require.Equal(t, "stored", got.Keys[0].Name)

	c, err = orderby.NewTranslatorContext(open, orderby.WithDeniedStorageFields("secret"),
		orderby.WithFieldMapping(map[string]string{"alias": "secret"}))
	require.NoError(t, err)
	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "alias"}}})
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

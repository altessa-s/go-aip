// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"
)

// TestOrderPolicyFromSchema builds the order policy from the declared
// patterns and default order and binds it to a storage translator.
func TestOrderPolicyFromSchema(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "createTime", Kind: filter.FieldKindTimestamp},
		{Name: "secret", Kind: filter.FieldKindString, Hidden: true},
	}, []string{"createTime desc, id", "name"}, listing.NewProfile(listing.WithDefaultOrderBy("name")))
	require.NoError(t, err)

	p := s.OrderPolicy()
	require.NotNil(t, p)

	got, err := p.Validate(orderby.Spec{})
	require.NoError(t, err)
	require.Equal(t, []orderby.Key{{Name: "name"}}, got.Keys)

	for _, field := range []string{"id", "name", "createTime"} {
		require.True(t, p.IsFieldAllowed(field), field)
	}

	require.False(t, p.IsFieldAllowed("secret"))
	require.False(t, p.IsFieldAllowed("undeclared"))

	tr, err := sqlbase.NewOrderTranslator(p, postgres.OrderDialect(), sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
	require.NoError(t, err)

	clause, err := tr.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "createTime", Direction: orderby.DirectionDescending}}})
	require.NoError(t, err)
	require.Equal(t, `"created_at" DESC`, clause)

	_, err = tr.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "secret"}}})
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

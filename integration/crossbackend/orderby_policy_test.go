// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// TestOrderPolicySharedAcrossTranslators builds one policy and binds it to two
// backends: both accept and refuse the same orders, and each applies its
// own storage mapping.
func TestOrderPolicySharedAcrossTranslators(t *testing.T) {
	t.Parallel()

	p := orderbytest.MustPolicy(t,
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields("createdAt", "name"),
		orderby.WithDefaultOrder(orderby.Key{Name: "createdAt", Direction: orderby.DirectionDescending}),
	)

	pg, err := sqlbase.NewOrderTranslator(p, postgres.OrderDialect(), sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createdAt": "created_at"})))
	require.NoError(t, err)
	ms, err := meili.NewOrderTranslator(p)
	require.NoError(t, err)

	for _, tc := range []struct {
		in    string
		sql   string
		meili []string
		err   error
	}{
		{in: "", sql: `"created_at" DESC`, meili: []string{"createdAt:desc"}},
		{in: "name, createdAt", sql: `"name" ASC, "created_at" ASC`, meili: []string{"name:asc", "createdAt:asc"}},
		{in: "secret", err: orderby.ErrFieldNotAllowed},
		{in: "name, secret desc", err: orderby.ErrFieldNotAllowed},
	} {
		spec := orderbytest.MustParseOrderBy(t, tc.in)

		_, policyErr := p.Validate(spec)
		require.ErrorIs(t, policyErr, tc.err, tc.in)

		gotSQL, err := pg.Translate(spec)
		require.ErrorIs(t, err, tc.err, tc.in)
		require.Equal(t, tc.sql, gotSQL, tc.in)

		gotMeili, err := ms.Translate(spec)
		require.ErrorIs(t, err, tc.err, tc.in)
		require.Equal(t, tc.meili, gotMeili, tc.in)
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestOrderingAttributes(t *testing.T) {
	t.Parallel()

	spec := orderby.Spec{Keys: []orderby.Key{{Name: "name", Nulls: orderby.NullsLast, Collation: "C"}}}
	p, err := sqlbase.NewOrderTranslator(orderbytest.MustPolicy(t), postgres.OrderDialect())
	require.NoError(t, err)
	sql, err := p.Translate(spec)
	require.NoError(t, err)
	require.Equal(t, `"name" COLLATE "C" ASC NULLS LAST`, sql)
	require.Equal(t, orderby.NullsFirst, spec.Reverse().Keys[0].Nulls)
}

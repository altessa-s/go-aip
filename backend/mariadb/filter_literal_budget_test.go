// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestSchemaLiteralBudgetOption(t *testing.T) {
	t.Parallel()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)

	node := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "long enough"}}

	for _, budget := range []int{2, 100} {
		contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true), mariadb.WithSchemaMaxDataBudget(budget))
		require.NoError(t, err)
		tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
		require.NoError(t, err)

		_, _, err = tr.Translate(node)
		if budget == 2 {
			require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
		} else {
			require.NoError(t, err)
		}
	}
}

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

func TestSizeBytesLiteral(t *testing.T) {
	t.Parallel()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"data": sqlbase.Scalar("data", sqlbase.Bytes)})
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
	require.NoError(t, err)

	node := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.CallNode{Op: filter.OpSize, Target: &filter.LiteralNode{Value: []byte("é")}}, Right: &filter.LiteralNode{Value: int64(2)}}
	query, _, err := tr.Translate(node)
	require.NoError(t, err)
	require.Contains(t, query, "OCTET_LENGTH(?)")
}

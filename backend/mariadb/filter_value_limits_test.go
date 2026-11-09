// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func TestConfiguredValueDepth(t *testing.T) {
	t.Parallel()

	value := "value"

	for _, depth := range []int{1, 2} {
		c := contractValidator{options: newSchemaOptions(WithSchemaMaxValueDepth(depth)), remaining: 100}

		err := c.validateLiteral(&value, sqlbase.Text)
		if depth == 1 {
			require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestConfiguredArrayDimensions(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"age": sqlbase.Scalar("age", sqlbase.Integer)})
	require.NoError(t, err)

	list := &filter.ListNode{Elements: []filter.Node{&filter.ListNode{Elements: []filter.Node{&filter.LiteralNode{Value: int64(1)}}}}}
	node := &filter.BinaryOpNode{Op: filter.OpIn, Left: &filter.IdentNode{Name: "age"}, Right: list}

	for _, dimensions := range []int{1, 2} {
		sem, err := NewSchema(schema, WithSchemaContractMode(true), WithSchemaMaxArrayDimensions(dimensions))
		require.NoError(t, err)
		tr, err := NewFilterTranslatorWithSchema(policy, sem)
		require.NoError(t, err)

		_, err = tr.validate(node)
		if dimensions == 1 {
			require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
		} else {
			require.NoError(t, err)
		}
	}
}

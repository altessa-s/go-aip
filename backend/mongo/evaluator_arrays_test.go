// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// TestEvaluator_ArrayFieldNotZeroWhenAbsent: a field declared by WithArrayFields and WithSchemaZeroWhenAbsent is refused: an
// absent array is empty, not a stored zero element.
func TestEvaluator_ArrayFieldNotZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithArrayFields("tags"), filter.WithAnyField(true))
	require.NoError(t, err)
	semantics, err := mongo.NewSchema(mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"tags": filter.FieldKindString}))
	require.NoError(t, err)
	err = semantics.ValidatePolicy(policy)
	require.ErrorIs(t, err, filter.ErrInvalidOptions, "an absent array is empty, not a stored zero element")
	_, err = newEvaluator(withPolicy(filter.WithArrayFields("tags")), filtereval.WithProfile(semantics))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

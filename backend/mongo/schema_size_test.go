// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestCheckSizeOperand(t *testing.T) {
	t.Parallel()

	for _, v := range []any{int64(0), int64(mongo.MaxSizeOperand), uint64(7), 0.5, int32(3), 9} {
		require.NoError(t, mongo.CheckSizeOperand("tags", v), "%v", v)
	}

	for _, v := range []any{int64(-1), int64(mongo.MaxSizeOperand) + 1, uint64(1) << 40, -0.5, math.NaN(), math.Inf(1), "1", nil, true} {
		err := mongo.CheckSizeOperand("tags", v)
		require.ErrorIs(t, err, filter.ErrInvalidExpression, "%v", v)
		require.ErrorIs(t, err, coreerrs.ErrInvalidArgument, "%v", v)
		fe, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok, "%v: a field error", v)
		require.Equal(t, "tags", fe.Field)
	}
}

func TestSchemaRejectsConflictingPresenceDeclarations(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		opts   []mongo.SchemaOption
		policy []filter.PolicyOption
	}{
		{
			name: "zero when absent",
			opts: []mongo.SchemaOption{
				mongo.WithSchemaPresenceFields("score"),
				mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"score": filter.FieldKindInt}),
			},
		},
		{
			name: "omit empty",
			opts: []mongo.SchemaOption{
				mongo.WithSchemaPresenceFields("score"),
				mongo.WithSchemaOmitEmpty("score"),
			},
		},
		{
			name:   "array",
			opts:   []mongo.SchemaOption{mongo.WithSchemaPresenceFields("score")},
			policy: []filter.PolicyOption{filter.WithArrayFields("score")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema, err := mongo.NewSchema(tc.opts...)
			if err == nil {
				policy, policyErr := filter.NewPolicy(append([]filter.PolicyOption{filter.WithAnyField(true)}, tc.policy...)...)
				require.NoError(t, policyErr)

				err = schema.ValidatePolicy(policy)
			}

			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func TestNewSchemaRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	for name, opts := range map[string][]mongo.SchemaOption{
		"blank zero-when-absent field":  {mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{" ": filter.FieldKindInt})},
		"unknown zero-when-absent kind": {mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"n": filter.FieldKindDecimal + 1})},
		"blank omitempty field":         {mongo.WithSchemaOmitEmpty("")},
		"blank presence field":          {mongo.WithSchemaPresenceFields("")},
		"presence and zero-when-absent": {
			mongo.WithSchemaPresenceFields("n"), mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"n": filter.FieldKindInt}),
		},
		"presence and omitempty": {mongo.WithSchemaPresenceFields("n"), mongo.WithSchemaOmitEmpty("n")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := mongo.NewSchema(opts...)
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}
}

func TestSchemaValidatePolicyRejectsConflicts(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		schema []mongo.SchemaOption
		policy []filter.PolicyOption
	}{
		"zero-when-absent array": {
			schema: []mongo.SchemaOption{mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"tags": filter.FieldKindString})},
			policy: []filter.PolicyOption{filter.WithArrayFields("tags")},
		},
		"conflicting kinds": {
			schema: []mongo.SchemaOption{mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"n": filter.FieldKindInt})},
			policy: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"n": filter.FieldKindString})},
		},
		"presence array": {
			schema: []mongo.SchemaOption{mongo.WithSchemaPresenceFields("tags")},
			policy: []filter.PolicyOption{filter.WithArrayFields("tags")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			schema := mustMongoSchema(t, tc.schema...)
			policy := filtertest.MustPolicy(t, append([]filter.PolicyOption{filter.WithAnyField(true)}, tc.policy...)...)
			require.ErrorIs(t, schema.ValidatePolicy(policy), filter.ErrInvalidOptions)

			_, err := filtereval.New(policy, filtereval.WithProfile(schema))
			require.ErrorIs(t, err, filter.ErrInvalidOptions, "the evaluator binds the schema to its policy")
		})
	}

	require.ErrorIs(t, mongo.DefaultSchema().ValidatePolicy(nil), filter.ErrInvalidOptions)
	require.ErrorIs(t, (*mongo.Schema)(nil).ValidatePolicy(filtertest.MustPolicy(t, filter.WithAnyField(true))), filter.ErrInvalidOptions)
}

func TestSchemaAccessorsOnNil(t *testing.T) {
	t.Parallel()

	var schema *mongo.Schema

	_, ok := schema.ZeroWhenAbsent("n")
	require.False(t, ok)
	require.False(t, schema.IsPresenceCall(nil))
	require.False(t, mustMongoSchema(t, mongo.WithSchemaPresenceFields("n")).IsPresenceCall(&filter.CallNode{Target: &filter.LiteralNode{}}))
	require.False(t, mongo.DefaultSchema().MeasuresResolvedSize("n", nil))
}

func TestSchemaValidateDecimal(t *testing.T) {
	t.Parallel()

	schema := mongo.DefaultSchema()
	require.NoError(t, schema.ValidateDecimal(decimal.RequireFromString("1.5")))
	require.ErrorIs(t, schema.ValidateDecimal(decimal.New(1, 7000)), filter.ErrFieldTypeMismatch, "beyond Decimal128's exponent range")
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestMongoValidationRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		node       filter.Node
		expression string
		fields     []string
		kinds      map[string]filter.FieldKind
		wantErr    error
	}{
		{name: "null against a computed value", expression: `name.substring(0) == null`, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "literal in field position", expression: `1 == name`, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "function in field position", expression: `name.contains("x") == true`, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "field not allowed", expression: `hidden == 1`, fields: []string{"name"}, wantErr: filter.ErrFieldNotAllowed},
		{name: "invalid field position", expression: `true == 1`, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "unsupported literal type", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: map[string]any{}}}, fields: []string{"name"}, wantErr: filter.ErrUnsupportedType},
		{name: "invalid decimal operand", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "price"}, Right: &filter.LiteralNode{Value: float64(1), Text: "not-a-decimal"}}, fields: []string{"price"}, kinds: map[string]filter.FieldKind{"price": filter.FieldKindDecimal}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "in list required", expression: `name in 1`, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "string function arity", node: &filter.CallNode{Op: filter.OpContains, Target: &filter.IdentNode{Name: "name"}}, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "string function argument type", node: &filter.CallNode{Op: filter.OpContains, Target: &filter.IdentNode{Name: "name"}, Args: []filter.Node{&filter.LiteralNode{Value: int64(1)}}}, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "size outside comparison", node: &filter.CallNode{Op: filter.OpSize, Target: &filter.IdentNode{Name: "name"}}, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "size without target", node: &filter.CallNode{Op: filter.OpSize}, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "invalid Mongo size operand", node: &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.CallNode{Op: filter.OpSize, Target: &filter.IdentNode{Name: "name"}}, Right: &filter.LiteralNode{Value: int64(-1)}}, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
		{name: "invalid regex", expression: `name.matches("[")`, fields: []string{"name"}, wantErr: filter.ErrInvalidRegex},
		{name: "unsupported function", node: &filter.CallNode{Op: filter.OpSubstring, Target: &filter.IdentNode{Name: "name"}}, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "unsupported unary operator", node: &filter.UnaryOpNode{Op: filter.OpSize, Operand: &filter.IdentNode{Name: "name"}}, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "unsupported comparison operator", node: &filter.BinaryOpNode{Op: filter.OpSubstring, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: int64(1)}}, fields: []string{"name"}, wantErr: filter.ErrUnsupportedOperation},
		{name: "non-predicate root", node: &filter.LiteralNode{Value: true}, fields: []string{"name"}, wantErr: filter.ErrInvalidExpression},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := []filter.PolicyOption{filter.WithAllowedFields(tt.fields...)}
			if len(tt.kinds) > 0 {
				policy = append(policy, filter.WithFieldTypes(tt.kinds))
			}

			eval, err := evaluator.New(filtertest.MustPolicy(t, policy...), evaluator.WithProfile(mongo.DefaultSchema()))
			require.NoError(t, err)

			node := tt.node
			if tt.expression != "" {
				node = filtertest.MustParseFilter(t, tt.expression)
			}

			require.ErrorIs(t, eval.Validate(node), tt.wantErr)
		})
	}
}

func TestMongoValidationChecksLimitsBeforeBackendWalk(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("name"), filter.WithMaxDepth(2)),
		evaluator.WithProfile(mongo.DefaultSchema()),
	)
	require.NoError(t, err)

	node := filter.Node(&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "x"}})
	for range 4 {
		node = &filter.UnaryOpNode{Op: filter.OpNot, Operand: node}
	}

	require.ErrorIs(t, eval.Validate(node), filter.ErrMaxDepthExceeded)
}

func TestMongoPresenceNNFDoesNotConsumeExtraOperationBudget(t *testing.T) {
	t.Parallel()

	semantics, err := mongo.NewSchema(mongo.WithSchemaPresenceFields("active"))
	require.NoError(t, err)
	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("active"), filter.WithFieldTypes(map[string]filter.FieldKind{"active": filter.FieldKindBool}), filter.WithMaxOperations(2)),
		evaluator.WithProfile(semantics),
	)
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `!active`)
	require.NoError(t, eval.Validate(node))
	prepared, err := eval.Prepare(node)
	require.NoError(t, err)
	got, err := eval.Evaluate(node, map[string]any{"active": true})
	require.NoError(t, err)
	require.False(t, got)
	got, err = prepared.Evaluate(map[string]any{"active": true})
	require.NoError(t, err)
	require.False(t, got)
}

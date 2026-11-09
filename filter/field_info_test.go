// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

func TestFieldPolicyIsBoundToItsPolicy(t *testing.T) {
	t.Parallel()

	restricted, err := filter.NewPolicy(filter.WithAllowedFields("name"))
	require.NoError(t, err)
	permissive, err := filter.NewPolicy(filter.WithAllowedFields("hidden"))
	require.NoError(t, err)
	require.True(t, permissive.Field("hidden").Allowed())
	require.False(t, restricted.Field("hidden").Allowed())
	field := permissive.Field("hidden")
	require.True(t, field.Allowed())
	field = filter.FieldPolicy{}
	require.False(t, field.Allowed())
	require.True(t, permissive.Field("hidden").Allowed(), "caller copies cannot mutate the policy")
}

func TestFieldPolicyUndeclaredAndMapped(t *testing.T) {
	t.Parallel()

	shared, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"age": filter.FieldKindInt}))
	require.NoError(t, err)
	policy, err := filter.NewTranslatorContext(shared, filter.WithFieldMapping(map[string]string{"age": "stored.age"}))
	require.NoError(t, err)

	field := policy.Field("unknown")
	require.True(t, field.Allowed())
	require.Equal(t, "unknown", field.Path())
	require.False(t, field.IsArray())
	require.NoError(t, field.CheckLiteral(&filter.LiteralNode{Value: "x"}))
	require.Equal(t, "stored.age", policy.Field("age").Path())
	require.Equal(t, "age", shared.Field("age").Path(), "binding leaves the shared policy unmapped")
	require.ErrorIs(t, policy.Field("age").CheckLiteral(&filter.LiteralNode{Value: "x"}), filter.ErrFieldTypeMismatch)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
)

func TestWithSchemaZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	fields := map[string]filter.FieldKind{
		"description": filter.FieldKindString,
		"retries":     filter.FieldKindInt,
		"ignored":     filter.FieldKindUnspecified,
	}
	ctx, err := mongo.NewSchema(mongo.WithSchemaZeroWhenAbsent(fields))
	require.NoError(t, err)

	fields["late"] = filter.FieldKindBool // the option copies the map

	for field, want := range map[string]filter.FieldKind{
		"description": filter.FieldKindString,
		"retries":     filter.FieldKindInt,
	} {
		kind, ok := ctx.ZeroWhenAbsent(field)
		require.True(t, ok, field)
		require.Equal(t, want, kind, field)
	}

	for _, field := range []string{"ignored", "late", "other"} {
		kind, ok := ctx.ZeroWhenAbsent(field)
		require.False(t, ok, field)
		require.Equal(t, filter.FieldKindUnspecified, kind, field)
	}

	bare, err := mongo.NewSchema()
	require.NoError(t, err)

	_, ok := bare.ZeroWhenAbsent("description")
	require.False(t, ok, "nothing is declared without the option")
}

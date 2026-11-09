// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func fieldInfoFields() []listing.Field {
	return []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindString, Enum: map[string]any{"KIND_A": "a", "KIND_B": "b"}},
		{
			Name: "status", Kind: filter.FieldKindInt, Optional: true, EnumZero: "STATUS_UNSPECIFIED",
			Enum: map[string]any{"STATUS_UNSPECIFIED": 0, "ACTIVE": 1},
		},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
		{Name: "note", Kind: filter.FieldKindString, Optional: true, Hidden: true},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
	}
}

func TestSchemaFieldInfoMatchesField(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema(fieldInfoFields(), []string{"id"}, nil)

	for _, name := range schema.Fields() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, ok := schema.Field(name)
			require.True(t, ok)

			got, ok := schema.FieldInfo(name)
			require.True(t, ok)
			require.Equal(t, listing.FieldInfo{
				Name: want.Name, Kind: want.Kind, EnumZero: want.EnumZero, Enum: want.Enum != nil, Array: want.Array,
				Optional: want.Optional, Presence: want.Presence, Hidden: want.Hidden, EnumZeroValue: want.Enum[want.EnumZero],
			}, got)
		})
	}
}

func TestSchemaFieldInfoEnumZeroIsStoredValue(t *testing.T) {
	t.Parallel()

	got, ok := listing.MustSchema(fieldInfoFields(), []string{"id"}, nil).FieldInfo("status")
	require.True(t, ok)
	require.Equal(t, int64(0), got.EnumZeroValue)
}

func TestSchemaFieldInfoRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		schema *listing.Schema
		name   string
		field  string
	}{
		{name: "undeclared field", schema: listing.MustSchema(fieldInfoFields(), []string{"id"}, nil), field: "missing"},
		{name: "nil schema", schema: nil, field: "id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.schema.FieldInfo(tt.field)
			require.False(t, ok)
			require.Zero(t, got)
		})
	}
}

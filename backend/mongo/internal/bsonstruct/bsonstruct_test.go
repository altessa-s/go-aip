// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package bsonstruct_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonstruct"
)

type leaf struct {
	A int
}

type fields struct {
	Plain     int
	Named     int            `bson:"n"`
	Skipped   int            `bson:"-"`
	Inline    leaf           `bson:",inline"`
	ByName    leaf           `bson:"inline"`
	Pointer   *leaf          `bson:",omitempty,inline"`
	Map       map[string]any `bson:",inline"`
	NotStruct *int           `bson:",inline"`
}

// unusualFields returns a struct type with a field whose tag has no key, which the driver reads as its bson tag, and an
// unexported inline field, which it skips.
func unusualFields() reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{Name: "Bare", Type: reflect.TypeFor[int](), Tag: "custom"},
		{Name: "hidden", PkgPath: "bsonstruct_test", Type: reflect.TypeFor[leaf](), Tag: `bson:",inline"`},
	})
}

// TestReadField pins how the driver's default tag parser reads a field: its key, whether it is encoded and which struct type it
// describes in place.
func TestReadField(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[fields]()
	leafType := reflect.TypeFor[leaf]()

	for name, want := range map[string]bsonstruct.Field{
		"Plain":     {Encoded: true, Key: "plain", KeyBytes: len("plain")},
		"Named":     {Encoded: true, Key: "n", KeyBytes: 1},
		"Skipped":   {},
		"Inline":    {Encoded: true, Inlined: leafType},
		"ByName":    {Encoded: true, Inlined: leafType},
		"Pointer":   {Encoded: true, Inlined: leafType},
		"Map":       {Encoded: true},
		"NotStruct": {Encoded: true},
	} {
		f, ok := typ.FieldByName(name)
		require.True(t, ok, name)
		assert.Equal(t, want, bsonstruct.ReadField(f), name)
	}

	unusual := unusualFields()
	assert.Equal(t, bsonstruct.Field{Encoded: true, Key: "custom", KeyBytes: len("custom")}, bsonstruct.ReadField(unusual.Field(0)))
	assert.Equal(t, bsonstruct.Field{}, bsonstruct.ReadField(unusual.Field(1)))
}

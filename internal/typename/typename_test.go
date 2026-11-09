// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package typename_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/typename"
)

func TestOf(t *testing.T) {
	t.Parallel()

	require.Equal(t, "<nil>", typename.Of(nil))
	require.Equal(t, "int64", typename.Of(int64(1)))
	require.Equal(t, "map[string]interface {}", typename.Of(map[string]any{}))

	fields := make([]reflect.StructField, 20)
	for i := range fields {
		fields[i] = reflect.StructField{Name: "Field" + strings.Repeat("x", i), Type: reflect.TypeFor[string](), Tag: `json:"f"`}
	}

	long := reflect.New(reflect.StructOf(fields)).Elem().Interface()
	require.Greater(t, len(reflect.TypeOf(long).String()), 128)

	got := typename.Of(long)
	require.Len(t, got, 128+len("..."))
	require.Equal(t, reflect.TypeOf(long).String()[:128]+"...", got)
}

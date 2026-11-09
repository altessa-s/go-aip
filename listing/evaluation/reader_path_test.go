// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

type keyName string

// TestReaderPathSegments pins the dotted path semantics of strings.Split: an empty path and an empty trailing segment are
// segments of their own, on every map shape the reader indexes.
func TestReaderPathSegments(t *testing.T) {
	t.Parallel()

	value := "v"
	tests := []struct {
		name    string
		data    map[string]any
		path    string
		want    any
		present bool
	}{
		{name: "empty path", data: map[string]any{"": 1}, path: "", want: 1, present: true},
		{name: "empty trailing segment", data: map[string]any{"a": map[string]any{"": 2}}, path: "a.", want: 2, present: true},
		{name: "missing trailing segment", data: map[string]any{"a": map[string]any{"b": 2}}, path: "a.", present: false},
		{name: "string map", data: map[string]any{"a": map[string]string{"b": "c"}}, path: "a.b", want: "c", present: true},
		{name: "string map whole", data: map[string]any{"a": map[string]string{"b": "c"}}, path: "a", want: map[string]string{"b": "c"}, present: true},
		{name: "string map too deep", data: map[string]any{"a": map[string]string{"b": "c"}}, path: "a.b.c", present: false},
		{name: "named key map", data: map[string]any{"a": map[keyName]int{"b": 3}}, path: "a.b", want: 3, present: true},
		{name: "int key map", data: map[string]any{"a": map[int]int{1: 3}}, path: "a.1", present: false},
		{name: "pointer leaf", data: map[string]any{"a": &value}, path: "a", want: "v", present: true},
		{name: "nil pointer", data: map[string]any{"a": (*string)(nil)}, path: "a", want: nil, present: true},
		{name: "nil pointer inner", data: map[string]any{"a": (*map[string]any)(nil)}, path: "a.b", want: nil, present: false},
		{name: "array fan-out", data: map[string]any{"a": []map[string]any{{"b": 1}, {"c": 2}, {"b": 3}}}, path: "a.b", want: []any{1, 3}, present: true},
		{name: "nil map", data: map[string]any{"a": map[string]any(nil)}, path: "a", want: map[string]any(nil), present: true},
	}

	encodings := map[string]evaluator.ValueEncoding{"plain": nil, "encoded": testEncoding{}}

	for _, tt := range tests {
		for encodingName, encoding := range encodings {
			t.Run(tt.name+"/"+encodingName, func(t *testing.T) {
				t.Parallel()

				got, present, err := evaluation.NewReader(encoding, 100).Lookup(tt.data, tt.path)
				require.NoError(t, err)
				require.Equal(t, tt.present, present)
				require.Equal(t, tt.want, got)
			})
		}
	}
}

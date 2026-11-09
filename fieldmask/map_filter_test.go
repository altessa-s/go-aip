// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func shelves(names ...string) map[string]*testpb.Shelf {
	out := make(map[string]*testpb.Shelf, len(names))
	for _, n := range names {
		out[n] = &testpb.Shelf{DisplayName: "d-" + n, Street: "s-" + n, Position: 1}
	}

	return out
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(m)
	require.NoError(t, err)

	return s
}

// TestFilter_MapNamedKeys pins the filter of map fields whose sub-mask names
// keys without "*": only the named entries survive, a branch rule masks the
// entry's value, and the result matches the entry-by-entry filter for every
// key kind.
func TestFilter_MapNamedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    func(t *testing.T) proto.Message
		paths []string
		want  func(t *testing.T) proto.Message
	}{
		{
			name:  "string keys, leaf rules",
			in:    func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a", "b", "c", "d")} },
			paths: []string{"shelves_by_name.a", "shelves_by_name.c"},
			want:  func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a", "c")} },
		},
		{
			name:  "string keys, branch rule masks the value",
			in:    func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a", "b", "c")} },
			paths: []string{"shelves_by_name.b.street"},
			want: func(*testing.T) proto.Message {
				return &testpb.Catalog{ShelvesByName: map[string]*testpb.Shelf{"b": {Street: "s-b"}}}
			},
		},
		{
			name:  "named key absent from the map",
			in:    func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a", "b")} },
			paths: []string{"shelves_by_name.zz"},
			want:  func(*testing.T) proto.Message { return &testpb.Catalog{} },
		},
		{
			name:  "quoted key with a dot",
			in:    func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a.b", "a", "b")} },
			paths: []string{"shelves_by_name.`a.b`"},
			want:  func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a.b")} },
		},
		{
			name:  "as many keys as entries",
			in:    func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a", "b")} },
			paths: []string{"shelves_by_name.a", "shelves_by_name.zz"},
			want:  func(*testing.T) proto.Message { return &testpb.Catalog{ShelvesByName: shelves("a")} },
		},
		{
			name: "scalar values, branch rule keeps the entry",
			in: func(*testing.T) proto.Message {
				return &testpb.UpdateRequest{Metadata: map[string]string{"a": "1", "b": "2", "c": "3"}}
			},
			paths: []string{"metadata.b"},
			want:  func(*testing.T) proto.Message { return &testpb.UpdateRequest{Metadata: map[string]string{"b": "2"}} },
		},
		{
			name: "int keys",
			in: func(*testing.T) proto.Message {
				return &testpb.Catalog{ShelvesById: map[int32]*testpb.Shelf{1: {DisplayName: "1"}, 2: {DisplayName: "2"}, 3: {DisplayName: "3"}}}
			},
			paths: []string{"shelves_by_id.2"},
			want: func(*testing.T) proto.Message {
				return &testpb.Catalog{ShelvesById: map[int32]*testpb.Shelf{2: {DisplayName: "2"}}}
			},
		},
		{
			name: "struct values drop what the mask addresses nothing of",
			in: func(t *testing.T) proto.Message {
				return &testpb.UpdateRequest{StructMap: map[string]*structpb.Struct{
					"a": mustStruct(t, map[string]any{"x": 1, "y": 2}),
					"b": mustStruct(t, map[string]any{"x": 3}),
					"c": mustStruct(t, map[string]any{"x": 4}),
				}}
			},
			paths: []string{"struct_map.a.x", "struct_map.b"},
			want: func(t *testing.T) proto.Message {
				return &testpb.UpdateRequest{StructMap: map[string]*structpb.Struct{
					"a": mustStruct(t, map[string]any{"x": 1}),
					"b": mustStruct(t, map[string]any{"x": 3}),
				}}
			},
		},
		{
			name: "dynamic message",
			in: func(*testing.T) proto.Message {
				return asDynamic(&testpb.Catalog{ShelvesByName: shelves("a", "b", "c")})
			},
			paths: []string{"shelves_by_name.c.display_name"},
			want: func(*testing.T) proto.Message {
				return asDynamic(&testpb.Catalog{ShelvesByName: map[string]*testpb.Shelf{"c": {DisplayName: "d-c"}}})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg := tt.in(t)
			require.NoError(t, fieldmask.FromPaths(tt.paths...).Filter(msg))
			require.True(t, proto.Equal(tt.want(t), msg), "got %v", msg)
		})
	}
}

func asDynamic(m proto.Message) *dynamicpb.Message {
	out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
	proto.Merge(out, m)

	return out
}

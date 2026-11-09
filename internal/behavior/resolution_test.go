// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/altessa-s/go-aip/internal/behavior"
)

func TestUnknownAnnotationAndOwnedResults(t *testing.T) {
	t.Parallel()

	options := &descriptorpb.FieldOptions{}
	proto.SetExtension(options, annotations.E_FieldBehavior, []annotations.FieldBehavior{99})
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("custom_behavior.proto"), Package: new("custombehavior"), Syntax: new("proto3"), Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Resource"), Field: []*descriptorpb.FieldDescriptorProto{{
			Name: new("value"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Options: options,
		}}}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)

	md := file.Messages().Get(0)
	require.True(t, behavior.SubtreeHasAny(md, 99))
	got := behavior.Get(md.Fields().Get(0))
	got[0] = annotations.FieldBehavior_REQUIRED

	require.Equal(t, []annotations.FieldBehavior{99}, behavior.Get(md.Fields().Get(0)))
}

func TestBehaviorPriority(t *testing.T) {
	t.Parallel()

	set := []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED, annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_OUTPUT_ONLY}
	require.Equal(t, annotations.FieldBehavior_OUTPUT_ONLY, behavior.FirstMatch(set, set))
	require.Equal(t, annotations.FieldBehavior_IDENTIFIER, behavior.FirstMatch(set[:3], set))
	require.Equal(t, annotations.FieldBehavior_IMMUTABLE, behavior.FirstMatch(set[:2], set))
	require.Equal(t, annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, behavior.FirstMatch(nil, set))
}

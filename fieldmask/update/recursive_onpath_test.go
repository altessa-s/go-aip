// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

// binaryNodeDescriptor builds Node{Node left; Node right; string server
// [OUTPUT_ONLY]; string value}: a message recursive through two fields.
func binaryNodeDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}

	server := field("server", 3, str, "")
	server.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(server.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY})

	file := &descriptorpb.FileDescriptorProto{
		Name: new("binarynode.proto"), Package: new("binarynode"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Node"), Field: []*descriptorpb.FieldDescriptorProto{
			field("left", 1, message, ".binarynode.Node"), field("right", 2, message, ".binarynode.Node"), server, field("value", 4, str, ""),
		}}},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Node")
}

// TestReplacementKeepsRecursionGuard pins the exact case of a full
// replacement of Node{Left: {Value: "l"}} without the stored resource: once
// the populated left branch is expanded, the absent right branch of the same
// recursive type stays guarded and is left as stored, not expanded by schema
// to right.value.
func TestReplacementKeepsRecursionGuard(t *testing.T) {
	t.Parallel()

	md := binaryNodeDescriptor(t)
	req := dynamicpb.NewMessage(md)
	left := req.Mutable(md.Fields().ByName("left")).Message()
	left.Set(md.Fields().ByName("value"), protoreflect.ValueOfString("l"))

	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, proto.Clone(req))
	require.NoError(t, err)
	require.Equal(t, []string{"left.value", "value"}, got.ToPaths())

	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"*"}})
	require.NoError(t, err)
	require.Equal(t, []string{"left.value", "value"}, plan.Paths)
}

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

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// metaChildDescriptor builds Root{Meta meta}, Meta{Child child},
// Child{string out [OUTPUT_ONLY]; string a; string b}.
func metaChildDescriptor(t *testing.T) protoreflect.MessageDescriptor {
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

	out := field("out", 1, str, "")
	out.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(out.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY})

	file := &descriptorpb.FileDescriptorProto{
		Name: new("metachild.proto"), Package: new("metachild"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{field("meta", 1, message, ".metachild.Meta")}},
			{Name: new("Meta"), Field: []*descriptorpb.FieldDescriptorProto{field("child", 1, message, ".metachild.Child")}},
			{Name: new("Child"), Field: []*descriptorpb.FieldDescriptorProto{out, field("a", 2, str, ""), field("b", 3, str, "")}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// TestValidationExpansionNotCounted pins that the path limit bounds the
// effective mask only: the exact cases of the implied plan of a Resource
// under WithMaxPaths(1), whose expansion of the whole resource only compares
// protected values, and of ["meta", "meta.child"] under WithMaxPaths(2), whose
// covered path expands to the same two leaves as its parent, are accepted.
func TestValidationExpansionNotCounted(t *testing.T) {
	t.Parallel()

	t.Run("implied plan", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(&pb.Resource{}, update.WithMaxPaths(1))
		require.NoError(t, err)

		plan, err := spec.Plan(&pb.Resource{Description: "d"}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"description"}, plan.Paths)
	})

	t.Run("covered expansion", func(t *testing.T) {
		t.Parallel()

		md := metaChildDescriptor(t)
		req := dynamicpb.NewMessage(md)
		meta := req.Mutable(md.Fields().ByName("meta")).Message()
		child := meta.Mutable(meta.Descriptor().Fields().ByName("child")).Message()
		child.Set(child.Descriptor().Fields().ByName("a"), protoreflect.ValueOfString("x"))

		paths := []string{"meta", "meta.child"}

		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), update.WithMaxPaths(2))
		require.NoError(t, err)
		require.Equal(t, []string{"meta.child.a", "meta.child.b"}, got.ToPaths())

		spec, err := update.NewSpec(dynamicpb.NewMessage(md), update.WithMaxPaths(2))
		require.NoError(t, err)

		plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths})
		require.NoError(t, err)
		require.Equal(t, []string{"meta.child.a", "meta.child.b"}, plan.Paths)

		// The effective mask is still held to the limit.
		_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), update.WithMaxPaths(1))
		require.Error(t, err)
	})
}

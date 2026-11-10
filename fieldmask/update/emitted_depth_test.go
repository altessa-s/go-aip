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

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// protectedOnlyDescriptor builds Root{Out out; Imm imm}, Out{string server
// [OUTPUT_ONLY]}, Imm{string code [IMMUTABLE]}: messages whose only field is
// protected, so their expansion emits no path.
func protectedOnlyDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string,
		behavior ...annotations.FieldBehavior,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if len(behavior) > 0 {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, behavior)
		}

		return fd
	}

	file := &descriptorpb.FileDescriptorProto{
		Name: new("protectedonly.proto"), Package: new("protectedonly"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{
				field("out", 1, message, ".protectedonly.Out"), field("imm", 2, message, ".protectedonly.Imm"),
			}},
			{Name: new("Out"), Field: []*descriptorpb.FieldDescriptorProto{field("server", 1, str, "", annotations.FieldBehavior_OUTPUT_ONLY)}},
			{Name: new("Imm"), Field: []*descriptorpb.FieldDescriptorProto{field("code", 1, str, "", annotations.FieldBehavior_IMMUTABLE)}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// TestProtectedOnlyExpansionAtDepthLimit pins that the path depth limit
// applies to the paths an expansion emits: under WithMaxPathDepth(1), a
// message at the limit whose only field is OUTPUT_ONLY, or IMMUTABLE and
// unchanged against the stored resource, expands to nothing, so ["out"],
// ["imm"] and ["*"] are accepted with an empty effective mask by ApplyMask,
// Apply, ValidateStrict and Spec.Plan.
func TestProtectedOnlyExpansionAtDepthLimit(t *testing.T) {
	t.Parallel()

	md := protectedOnlyDescriptor(t)
	build := func() *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		out := m.Mutable(md.Fields().ByName("out")).Message()
		out.Set(out.Descriptor().Fields().ByName("server"), protoreflect.ValueOfString("s"))

		imm := m.Mutable(md.Fields().ByName("imm")).Message()
		imm.Set(imm.Descriptor().Fields().ByName("code"), protoreflect.ValueOfString("c"))

		return m
	}

	limit := update.WithMaxPathDepth(1)
	current := update.WithCurrent(build())

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), limit)
	require.NoError(t, err)

	for _, path := range []string{"out", "imm", "*"} {
		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, build(), limit, current)
		require.NoError(t, err, path)
		require.Equal(t, []string{}, got.ToPaths(), path)

		require.NoError(t, update.Apply(fieldmask.FromPaths(path), build(), limit, current), path)

		strict, err := update.ValidateStrict(fieldmask.FromPaths(path), build(), limit, current)
		require.NoError(t, err, path)
		require.Equal(t, []string{}, strict.ToPaths(), path)

		plan, err := spec.Plan(build(), &fieldmaskpb.FieldMask{Paths: []string{path}}, current)
		require.NoError(t, err, path)
		require.True(t, plan.Empty(), path)
	}
}

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

// outputOnlyOneofDescriptor builds Oo{oneof a {string pinned [OUTPUT_ONLY];
// string free}; oneof b {string etag [OUTPUT_ONLY]; string alt}; oneof c
// {Inner audit [OUTPUT_ONLY]; string other}}, Inner{string imm [IMMUTABLE]}.
func outputOnlyOneofDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number, oneof int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string,
		behaviors ...annotations.FieldBehavior,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(jsonName(name)), Label: optional, Type: typ}
		if oneof >= 0 {
			fd.OneofIndex = new(oneof)
		}

		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if len(behaviors) > 0 {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, behaviors)
		}

		return fd
	}
	oo := annotations.FieldBehavior_OUTPUT_ONLY

	file := &descriptorpb.FileDescriptorProto{
		Name: new("oneofoo.proto"), Package: new("oneofoo"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      new("Oo"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("a")}, {Name: new("b")}, {Name: new("c")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					field("pinned", 1, 0, str, "", oo), field("free", 2, 0, str, ""),
					field("etag", 3, 1, str, "", oo), field("alt", 4, 1, str, ""),
					field("audit", 5, 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), ".oneofoo.Inner", oo), field("other", 6, 2, str, ""),
				},
			},
			{Name: new("Inner"), Field: []*descriptorpb.FieldDescriptorProto{field("imm", 1, -1, str, "", annotations.FieldBehavior_IMMUTABLE)}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Oo")
}

// TestOneofGuard_OutputOnlyMemberNotGuarded pins that an OUTPUT_ONLY oneof
// member is never guarded, whatever the policy names it or it holds: setting
// its sibling is accepted with the stored OUTPUT_ONLY member, for
// WithImmutableFields naming it (the exact case), the default identity name
// etag, and an OUTPUT_ONLY message holding an IMMUTABLE field, by every entry
// point and both guards.
func TestOneofGuard_OutputOnlyMemberNotGuarded(t *testing.T) {
	t.Parallel()

	md := outputOnlyOneofDescriptor(t)
	msg := func(values map[string]string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		for name, v := range values {
			fd := md.Fields().ByName(protoreflect.Name(name))
			if fd.Message() != nil {
				inner := dynamicpb.NewMessage(fd.Message())
				inner.Set(fd.Message().Fields().ByName("imm"), protoreflect.ValueOfString(v))
				m.Set(fd, protoreflect.ValueOfMessage(inner))

				continue
			}

			m.Set(fd, protoreflect.ValueOfString(v))
		}

		return m
	}

	for _, tc := range []struct {
		name   string
		stored string
		set    string
		opts   []update.Option
	}{
		{name: "named immutable by the policy", stored: "pinned", set: "free", opts: []update.Option{update.WithImmutableFields("pinned")}},
		{name: "default identity name", stored: "etag", set: "alt"},
		{name: "message holding an IMMUTABLE field", stored: "audit", set: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := msg(map[string]string{tc.stored: "old"})
			req := msg(map[string]string{tc.set: "new"})
			opts := append([]update.Option{update.WithCurrent(stored)}, tc.opts...)

			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.set}}, proto.Clone(req), opts...)
			require.NoError(t, err)
			require.Equal(t, []string{tc.set}, got.ToPaths())

			require.NoError(t, update.Apply(fieldmask.FromPaths(tc.set), proto.Clone(req), opts...))

			_, err = update.ValidateStrict(fieldmask.FromPaths(tc.set), req, opts...)
			require.NoError(t, err)

			spec, err := update.NewSpec(dynamicpb.NewMessage(md), tc.opts...)
			require.NoError(t, err)

			plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{tc.set}}, update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, []string{tc.set}, plan.Paths)
		})
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
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

// atomicOneofDescriptor builds Root{oneof choice {Owner owner; string free};
// oneof choice2 {Wrapper wrapper; string free2}}, Wrapper{Owner owner},
// Owner{string org [IMMUTABLE]; string email}: a protected atomic value as a
// oneof member, directly and below a wrapper.
func atomicOneofDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string, oneof int32) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if oneof >= 0 {
			fd.OneofIndex = new(oneof)
		}

		return fd
	}

	org := field("org", 1, str, "", -1)
	org.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(org.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE})

	file := &descriptorpb.FileDescriptorProto{
		Name: new("atomiconeof.proto"), Package: new("atomiconeof"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      new("Root"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("choice")}, {Name: new("choice2")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					field("owner", 1, message, ".atomiconeof.Owner", 0), field("free", 2, str, "", 0),
					field("wrapper", 3, message, ".atomiconeof.Wrapper", 1), field("free2", 4, str, "", 1),
				},
			},
			{Name: new("Wrapper"), Field: []*descriptorpb.FieldDescriptorProto{field("owner", 1, message, ".atomiconeof.Owner", -1)}},
			{Name: new("Owner"), Field: []*descriptorpb.FieldDescriptorProto{org, field("email", 2, str, "", -1)}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// TestOneofSwitchAwayFromProtectedAtomic pins the exact cases of a oneof
// switch away from a stored atomic value holding an IMMUTABLE org: setting
// free beside owner, and free2 beside a wrapper holding the atomic owner,
// would clear it, so the switch is refused, explicit or implied, with and
// without the stored resource, by ApplyMask, Apply, ValidateStrict and
// Spec.Plan.
func TestOneofSwitchAwayFromProtectedAtomic(t *testing.T) {
	t.Parallel()

	md := atomicOneofDescriptor(t)
	parse := func(text string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}
	atomic := atomicNames("atomiconeof.Owner")

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), atomic)
	require.NoError(t, err)

	for _, tc := range []struct{ name, stored, req, path string }{
		{name: "atomic member", stored: `owner { org: "corp" }`, req: `free: "x"`, path: "free"},
		{name: "wrapper member", stored: `wrapper { owner { org: "corp" } }`, req: `free2: "x"`, path: "free2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored, req := parse(tc.stored), parse(tc.req)

			for _, current := range [][]update.Option{{update.WithCurrent(stored)}, nil} {
				opts := append([]update.Option{atomic}, current...)

				for _, raw := range []*fieldmaskpb.FieldMask{{Paths: []string{tc.path}}, nil} {
					_, err := update.ApplyMask(raw, proto.Clone(req), opts...)
					require.ErrorIs(t, err, update.ErrBehaviorViolation)

					_, err = spec.Plan(req, raw, current...)
					require.ErrorIs(t, err, update.ErrProtectedField)
				}

				require.ErrorIs(t, update.Apply(fieldmask.FromPaths(tc.path), proto.Clone(req), opts...), update.ErrBehaviorViolation)

				_, err := update.ValidateStrict(fieldmask.FromPaths(tc.path), req, opts...)
				require.ErrorIs(t, err, update.ErrBehaviorViolation)
			}
		})
	}
}

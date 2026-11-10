// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
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

// protectedOutputOnlyDescriptor builds Root{Settings settings},
// Settings{Config config [IMMUTABLE]; repeated Config configs [IMMUTABLE];
// map<string, Config> named [IMMUTABLE]; string note}, Config{string server
// [OUTPUT_ONLY]; string x}: protected values holding server-managed fields.
func protectedOutputOnlyDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string, behavior annotations.FieldBehavior,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{behavior})
		}

		return fd
	}
	none := annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
	immutable := annotations.FieldBehavior_IMMUTABLE

	file := &descriptorpb.FileDescriptorProto{
		Name: new("protectedoo.proto"), Package: new("protectedoo"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{field("settings", 1, optional, message, ".protectedoo.Settings", none)}},
			{
				Name: new("Settings"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("config", 1, optional, message, ".protectedoo.Config", immutable),
					field("configs", 2, repeated, message, ".protectedoo.Config", immutable),
					field("named", 3, repeated, message, ".protectedoo.Settings.NamedEntry", immutable),
					field("note", 4, optional, str, "", none),
				},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    new("NamedEntry"),
					Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
					Field: []*descriptorpb.FieldDescriptorProto{
						field("key", 1, optional, str, "", none), field("value", 2, optional, message, ".protectedoo.Config", none),
					},
				}},
			},
			{Name: new("Config"), Field: []*descriptorpb.FieldDescriptorProto{
				field("server", 1, optional, str, "", annotations.FieldBehavior_OUTPUT_ONLY), field("x", 2, optional, str, "", none),
			}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// TestAtomicProtectedValueKeepsOutputOnly pins the exact case of an atomic
// settings whose IMMUTABLE config, configs and named hold an OUTPUT_ONLY
// server: echoed equal to the stored values, they pass the comparison, and
// clearing the request's OUTPUT_ONLY values must not turn them into different
// values. The merge of ApplyMask's request and effective mask, and Patch.Run,
// keep the stored server values bit-exact while writing the new note.
func TestAtomicProtectedValueKeepsOutputOnly(t *testing.T) {
	t.Parallel()

	md := protectedOutputOnlyDescriptor(t)
	parse := func(text string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}

	const protected = `config { server: "saved" x: "a" } configs { server: "s1" x: "b" } named { key: "k" value { server: "s2" x: "c" } }`

	stored := parse(`settings { ` + protected + ` note: "old" }`)
	req := parse(`settings { ` + protected + ` note: "new" }`)

	want := req
	atomic := atomicNames("protectedoo.Settings")

	msg := proto.Clone(req)
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"settings"}}, msg, atomic, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"settings"}, got.ToPaths())

	merged := proto.Clone(stored)
	require.NoError(t, update.MergePaths(merged, msg, got.ToPaths(), atomic))
	requireProtoEqual(t, want, merged)

	msg = proto.Clone(req)
	require.NoError(t, update.Apply(fieldmask.FromPaths("settings"), msg, atomic, update.WithCurrent(stored)))

	merged = proto.Clone(stored)
	require.NoError(t, update.MergePaths(merged, msg, []string{"settings"}, atomic))
	requireProtoEqual(t, want, merged)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), atomic)
	require.NoError(t, err)

	var saved *dynamicpb.Message

	patch := update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64]{
		Field: "root", Spec: spec,
		Precondition: update.Int64Precondition[*dynamicpb.Message]{Field: "root.etag", Version: func(*dynamicpb.Message) int64 { return 1 }},
		Read: func(context.Context) (*dynamicpb.Message, error) {
			return proto.Clone(stored).(*dynamicpb.Message), nil
		},
		ToProto: func(m *dynamicpb.Message) *dynamicpb.Message { return m },
		Save: func(_ context.Context, m *dynamicpb.Message, _ update.Plan, _ *int64) (*dynamicpb.Message, error) {
			saved = m
			return m, nil
		},
	}
	_, err = patch.Run(t.Context(), req, "", &fieldmaskpb.FieldMask{Paths: []string{"settings"}})
	require.NoError(t, err)
	requireProtoEqual(t, want, saved)

	// A changed protected value is still refused.
	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"settings"}},
		parse(`settings { config { server: "other" x: "a" } }`), atomic, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

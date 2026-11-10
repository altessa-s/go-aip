// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// atomicCollectionDescriptor builds Times{map<string, Timestamp> times;
// repeated Timestamp stamps}: collections of an atomic message.
func atomicCollectionDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	_ = timestamppb.Timestamp{} // registers google/protobuf/timestamp.proto

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	field := func(name string, number int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(jsonName(name)), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}

	file := &descriptorpb.FileDescriptorProto{
		Name: new("atomiccoll.proto"), Package: new("atomiccoll"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Times"),
			Field: []*descriptorpb.FieldDescriptorProto{
				field("times", 1, repeated, message, ".atomiccoll.Times.TimesEntry"),
				field("stamps", 2, repeated, message, ".google.protobuf.Timestamp"),
			},
			NestedType: []*descriptorpb.DescriptorProto{{
				Name:    new("TimesEntry"),
				Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
				Field: []*descriptorpb.FieldDescriptorProto{
					field("key", 1, optional, descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), ""),
					field("value", 2, optional, message, ".google.protobuf.Timestamp"),
				},
			}},
		}},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Times")
}

// TestAtomicCollectionElementsWhole pins that a key or "*" naming an atomic
// element whole is a whole-element replacement, accepted by every entry
// point — the exact cases ["times.k"] on a map<string, Timestamp> and
// ["stamps.*"] on a repeated Timestamp — while a path into such an element
// ("times.k.seconds", "stamps.*.seconds") is still refused.
func TestAtomicCollectionElementsWhole(t *testing.T) {
	t.Parallel()

	md := atomicCollectionDescriptor(t)
	build := func(seconds int64) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		ts := timestamppb.New(time.Unix(seconds, 0))
		m.Mutable(md.Fields().ByName("times")).Map().Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfMessage(ts.ProtoReflect()))
		m.Mutable(md.Fields().ByName("stamps")).List().Append(protoreflect.ValueOfMessage(timestamppb.New(time.Unix(seconds, 0)).ProtoReflect()))

		return m
	}

	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	for _, path := range []string{"times.k", "times.*", "stamps.*", "times", "stamps"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			stored, req := build(1), build(2)

			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, proto.Clone(req), update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, []string{path}, got.ToPaths())

			require.NoError(t, update.Apply(fieldmask.FromPaths(path), proto.Clone(req), update.WithCurrent(stored)))

			_, err = update.ValidateStrict(fieldmask.FromPaths(path), req, update.WithCurrent(stored))
			require.NoError(t, err)

			plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{path}}, update.WithCurrent(stored))
			require.NoError(t, err)

			merged := proto.Clone(stored)
			require.NoError(t, update.MergePaths(merged, req, plan.Paths))

			want := proto.Clone(stored).ProtoReflect()
			fd := md.Fields().ByName(protoreflect.Name(strings.Split(path, ".")[0]))
			want.Set(fd, req.Get(fd))
			requireProtoEqual(t, want.Interface(), merged)
		})
	}

	for _, path := range []string{"times.k.seconds", "stamps.*.seconds"} {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, build(2), update.WithCurrent(build(1)))
		require.ErrorIs(t, err, fieldmask.ErrInvalidPath, path)

		_, err = spec.Plan(build(2), &fieldmaskpb.FieldMask{Paths: []string{path}}, update.WithCurrent(build(1)))
		require.ErrorIs(t, err, fieldmask.ErrInvalidPath, path)
	}
}

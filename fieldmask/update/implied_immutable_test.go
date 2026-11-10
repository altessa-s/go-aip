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

	"github.com/altessa-s/go-aip/fieldmask/update"
)

// widgetDescriptor builds Widget{Meta meta}, Meta{string tier [IMMUTABLE];
// string email [REQUIRED]; string label1; string label2}: a sub-message with
// an IMMUTABLE field and no OUTPUT_ONLY or IDENTIFIER one.
func widgetDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, behaviors ...annotations.FieldBehavior) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: str}
		if len(behaviors) > 0 {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, behaviors)
		}

		return fd
	}
	meta := &descriptorpb.FieldDescriptorProto{Name: new("meta"), Number: new(int32(1)), JsonName: new("meta"), Label: optional,
		Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".widgettest.Meta")}

	file := &descriptorpb.FileDescriptorProto{
		Name: new("widgettest.proto"), Package: new("widgettest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Widget"), Field: []*descriptorpb.FieldDescriptorProto{meta}},
			{Name: new("Meta"), Field: []*descriptorpb.FieldDescriptorProto{
				field("tier", 1, annotations.FieldBehavior_IMMUTABLE), field("email", 2, annotations.FieldBehavior_REQUIRED),
				field("label1", 3), field("label2", 4),
			}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Widget")
}

// TestImpliedMaskEchoedImmutable pins the exact case of an omitted mask whose
// request echoes an unchanged nested IMMUTABLE value: Meta{Tier: "free",
// Label1: "new"} against a stored Meta with tier free, email and label2. The
// implied mask names the populated leaves (meta.label1, meta.tier), not meta
// whole, so the unchanged tier is ignored, the unset REQUIRED email and
// label2 keep their stored values, and only meta.label1 is written back.
func TestImpliedMaskEchoedImmutable(t *testing.T) {
	t.Parallel()

	md := widgetDescriptor(t)
	metaMD := md.Fields().ByName("meta").Message()
	widget := func(values map[string]string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(metaMD)
		for name, v := range values {
			m.Set(metaMD.Fields().ByName(protoreflect.Name(name)), protoreflect.ValueOfString(v))
		}

		w := dynamicpb.NewMessage(md)
		w.Set(md.Fields().ByName("meta"), protoreflect.ValueOfMessage(m))

		return w
	}

	stored := widget(map[string]string{"tier": "free", "email": "a@b.com", "label1": "old", "label2": "keep"})
	req := widget(map[string]string{"tier": "free", "label1": "new"})

	require.Equal(t, []string{"meta.label1", "meta.tier"}, update.FromSetFields(req).ToPaths())

	got, err := update.ApplyMask(nil, proto.Clone(req), update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"meta.label1"}, got.ToPaths())

	require.NoError(t, update.Apply(nil, proto.Clone(req), update.WithCurrent(stored)))

	strict, err := update.ValidateStrict(nil, req, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"meta.label1"}, strict.ToPaths())

	merged := proto.Clone(stored)
	require.NoError(t, update.Merge(got, merged, req))
	requireProtoEqual(t, widget(map[string]string{"tier": "free", "email": "a@b.com", "label1": "new", "label2": "keep"}), merged)

	// A changed IMMUTABLE value is still refused.
	_, err = update.ApplyMask(nil, widget(map[string]string{"tier": "pro", "label1": "new"}), update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

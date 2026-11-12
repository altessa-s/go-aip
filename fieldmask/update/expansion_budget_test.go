// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"runtime"
	"strconv"
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

// branchingLevels is the depth of the branching schema: 2^24 leaf paths.
const branchingLevels = 24

// branchingDescriptor builds an acyclic schema of levels messages, L0 to
// L<levels-1>, each holding two fields a and b of the next type, ending in
// Leaf{string out [OUTPUT_ONLY]; string v}: a populated "*" replacement has
// 2^levels leaves. Absent branches await stored-state expansion.
func branchingDescriptor(t testing.TB, levels int) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	field := func(name string, number int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()

	out := field("out", 1, str, "")
	out.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(out.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY})

	pkg := "branching" + strconv.Itoa(levels)
	msgs := []*descriptorpb.DescriptorProto{{Name: new("Leaf"), Field: []*descriptorpb.FieldDescriptorProto{out, field("v", 2, str, "")}}}

	for i := levels - 1; i >= 0; i-- {
		next := "." + pkg + ".Leaf"
		if i < levels-1 {
			next = "." + pkg + ".L" + strconv.Itoa(i+1)
		}

		msgs = append(msgs, &descriptorpb.DescriptorProto{
			Name:  new("L" + strconv.Itoa(i)),
			Field: []*descriptorpb.FieldDescriptorProto{field("a", 1, message, next), field("b", 2, message, next)},
		})
	}

	file := &descriptorpb.FileDescriptorProto{
		Name: new(pkg + ".proto"), Package: new(pkg), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"}, MessageType: msgs,
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("L0")
}

// TestReplacementExpansionBudget checks that a populated branching replacement
// stops at the first excess of the path limit, refused with the limit sentinel by
// ApplyMask, Apply, ValidateStrict and Spec.Plan within a small allocation
// bound; a message named whole of that schema is bounded the same way.
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestReplacementExpansionBudget(t *testing.T) { //nolint:paralleltest // see above
	// Populate 4096 leaves, enough to exceed the path limit under either "*"
	// or "a". An absent 24-level schema is tested separately below.
	md := branchingDescriptor(t, 12)
	req := populatedBranchingMessage(md)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{name: "ApplyMask", run: func() error {
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, proto.Clone(req), update.WithCurrent(req))
			return err
		}},
		{name: "ApplyMask parent", run: func() error {
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"a"}}, proto.Clone(req), update.WithCurrent(req))
			return err
		}},
		{name: "Apply", run: func() error { return update.Apply(fieldmask.FromPaths("*"), proto.Clone(req), update.WithCurrent(req)) }},
		{name: "ValidateStrict", run: func() error {
			_, err := update.ValidateStrict(fieldmask.FromPaths("*"), req, update.WithCurrent(req))
			return err
		}},
		{name: "Spec.Plan", run: func() error {
			_, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"*"}}, update.WithCurrent(req))
			return err
		}},
	} {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		err := tc.run()

		runtime.ReadMemStats(&after)

		require.ErrorIs(t, err, fieldmask.ErrMaxPathsExceeded, tc.name)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20), "%s allocated %d bytes", tc.name, after.TotalAlloc-before.TotalAlloc)
	}

	// A small populated branching schema still expands within the limits.
	small := branchingDescriptor(t, 3)
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, populatedBranchingMessage(small))
	require.NoError(t, err)
	require.Len(t, got.ToPaths(), 8)

	// Absent branches need no speculative traversal. Completion against an
	// equally empty stored resource must stay empty too.
	absent := dynamicpb.NewMessage(branchingDescriptor(t, branchingLevels))
	pending, err := update.Prepare(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, absent)
	require.NoError(t, err)
	require.Empty(t, pending.Mask())
	got, err = pending.Complete(dynamicpb.NewMessage(absent.Descriptor()))
	require.NoError(t, err)
	require.Empty(t, got)
	requireProtoEqual(t, dynamicpb.NewMessage(absent.Descriptor()), absent)
}

func populatedBranchingMessage(md protoreflect.MessageDescriptor) *dynamicpb.Message {
	m := dynamicpb.NewMessage(md)

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Message() != nil {
			m.Set(fd, protoreflect.ValueOfMessage(populatedBranchingMessage(fd.Message())))
		}
	}

	return m
}

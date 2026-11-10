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

// atomicLeafDescriptor builds Root{Atomic settings; Wrapper wrapper},
// Wrapper{Atomic owner; string w}, Atomic{repeated string tags [IMMUTABLE];
// map<string, string> labels [IMMUTABLE]; string org [IMMUTABLE];
// string note}: an atomic type with protected collections and a protected
// scalar, directly and below an unprotected wrapper.
func atomicLeafDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	immutable := []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
	field := func(name string, number int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string, behaviors []annotations.FieldBehavior,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if behaviors != nil {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, behaviors)
		}

		return fd
	}

	file := &descriptorpb.FileDescriptorProto{
		Name: new("atomicleaf.proto"), Package: new("atomicleaf"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{
				field("settings", 1, optional, message, ".atomicleaf.Atomic", nil),
				field("wrapper", 2, optional, message, ".atomicleaf.Wrapper", nil),
			}},
			{Name: new("Wrapper"), Field: []*descriptorpb.FieldDescriptorProto{
				field("owner", 1, optional, message, ".atomicleaf.Atomic", nil), field("w", 2, optional, str, "", nil),
			}},
			{
				Name: new("Atomic"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("tags", 1, repeated, str, "", immutable),
					field("labels", 2, repeated, message, ".atomicleaf.Atomic.LabelsEntry", immutable),
					field("org", 3, optional, str, "", immutable),
					field("note", 4, optional, str, "", nil),
				},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    new("LabelsEntry"),
					Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
					Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, optional, str, "", nil), field("value", 2, optional, str, "", nil)},
				}},
			},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// TestAtomicValueIsProtectedLeaf pins that an atomic value whose type holds a
// protected field is compared as a protected leaf wherever it is written
// whole: its IMMUTABLE repeated and map fields are compared whole (a changed
// or cleared one is refused, an equal one accepted) when it is named, and an
// atomic value below an unprotected wrapper is reached when the wrapper is
// named or "*" is sent, by ApplyMask, Apply, ValidateStrict and Spec.Plan.
func TestAtomicValueIsProtectedLeaf(t *testing.T) {
	t.Parallel()

	md := atomicLeafDescriptor(t)
	parse := func(text string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}
	atomic := atomicNames("atomicleaf.Atomic")
	stored := parse(`settings { tags: "a" labels { key: "k" value: "v" } org: "o" } wrapper { owner { org: "o" } w: "old" }`)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), atomic)
	require.NoError(t, err)

	for _, tc := range []struct {
		name, path, req string
		refused         bool
	}{
		{name: "changed list", path: "settings", req: `settings { tags: "b" labels { key: "k" value: "v" } org: "o" }`, refused: true},
		{name: "cleared list", path: "settings", req: `settings { labels { key: "k" value: "v" } org: "o" }`, refused: true},
		{name: "changed map", path: "settings", req: `settings { tags: "a" labels { key: "k" value: "x" } org: "o" }`, refused: true},
		{name: "equal", path: "settings", req: `settings { tags: "a" labels { key: "k" value: "v" } org: "o" note: "n" }`},
		{name: "wrapper", path: "wrapper", req: `wrapper { owner { org: "changed" } w: "new" }`, refused: true},
		{name: "wrapper equal", path: "wrapper", req: `wrapper { owner { org: "o" } w: "new" }`},
		{name: "full replacement", path: "*", req: `settings { tags: "a" labels { key: "k" value: "v" } org: "o" } wrapper { owner { org: "changed" } }`,
			refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := parse(tc.req)
			opts := []update.Option{atomic, update.WithCurrent(stored)}

			_, applyMaskErr := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.path}}, proto.Clone(req), opts...)
			applyErr := update.Apply(fieldmask.FromPaths(tc.path), proto.Clone(req), opts...)
			_, strictErr := update.ValidateStrict(fieldmask.FromPaths(tc.path), req, opts...)
			_, planErr := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{tc.path}}, update.WithCurrent(stored))

			if !tc.refused {
				require.NoError(t, applyMaskErr)
				require.NoError(t, applyErr)
				require.NoError(t, strictErr)
				require.NoError(t, planErr)

				return
			}

			require.ErrorIs(t, applyMaskErr, update.ErrBehaviorViolation)
			require.ErrorIs(t, applyErr, update.ErrBehaviorViolation)
			require.ErrorIs(t, strictErr, update.ErrBehaviorViolation)
			require.ErrorIs(t, planErr, update.ErrProtectedField)
		})
	}

	// The wrapper is expanded so the atomic owner is compared and its sibling
	// written: the effective mask names both.
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"wrapper"}}, parse(`wrapper { owner { org: "o" } w: "new" }`),
		atomic, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"wrapper.owner", "wrapper.w"}, got.ToPaths())
}

// atomicBranchingDescriptor builds Root{L0 a} over an acyclic schema of levels
// messages L0..L<levels-1>, each holding fields a and b of the next type,
// ending in Leaf{string code [IMMUTABLE]}, with L0 atomic.
func atomicBranchingDescriptor(t testing.TB, levels int) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	field := func(name string, number int32, typ *descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: optional, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}

	code := field("code", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), "")
	code.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(code.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE})

	pkg := "atomicbranching" + strconv.Itoa(levels)
	msgs := []*descriptorpb.DescriptorProto{
		{Name: new("Leaf"), Field: []*descriptorpb.FieldDescriptorProto{code}},
		{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{field("a", 1, message, "."+pkg+".L0")}},
	}

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

	return fd.Messages().ByName("Root")
}

// TestAtomicProtectedDiscoveryBounded pins the exact case of an atomic value
// over a schema branching at each of 24 levels down to an IMMUTABLE field:
// without the stored resource "*" is refused with one violation, found by a
// schema walk that visits each type once, within a small allocation bound.
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestAtomicProtectedDiscoveryBounded(t *testing.T) { //nolint:paralleltest // see above
	md := atomicBranchingDescriptor(t, 24)
	req := dynamicpb.NewMessage(md)
	req.Mutable(md.Fields().ByName("a"))

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, req, atomicNames("atomicbranching24.L0"))

	runtime.ReadMemStats(&after)

	require.Len(t, behaviorViolations(t, err), 1)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20), "allocated %d bytes", after.TotalAlloc-before.TotalAlloc)
}

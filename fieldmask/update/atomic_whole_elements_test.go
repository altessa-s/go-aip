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

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// atomicNames makes the named message types atomic, besides the default ones.
func atomicNames(names ...protoreflect.FullName) update.Option {
	return update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
		for _, n := range names {
			if md.FullName() == n {
				return true
			}
		}

		return update.DefaultAtomicMessage(md)
	})
}

// TestWholeAtomicElementsChecked pins that an element of an atomic type named
// whole is checked as on create, as the collection named whole is: the exact
// case ["delegates.k"] with Owner atomic and its REQUIRED email empty, and
// ["members.*"], are refused by ApplyMask, Apply, ValidateStrict, Spec.Plan
// and Patch.Run.
func TestWholeAtomicElementsChecked(t *testing.T) {
	t.Parallel()

	atomic := atomicNames("fieldbehaviortest.v1.Owner")

	for _, tc := range []struct {
		path string
		req  *pb.Account
		want string
	}{
		{path: "delegates.k", req: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Org: "o"}}}, want: "delegates.k.email"},
		{path: "members.*", req: &pb.Account{Members: []*pb.Owner{{Org: "o"}}}, want: "members.0.email"},
		{path: "delegates", req: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Org: "o"}}}, want: "delegates.k.email"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			want := []FieldViolation{{Path: tc.want, Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}

			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.path}}, proto.Clone(tc.req), atomic)
			require.Equal(t, want, behaviorViolations(t, err))

			require.Equal(t, want, behaviorViolations(t, update.Apply(fieldmask.FromPaths(tc.path), proto.Clone(tc.req), atomic)))

			_, err = update.ValidateStrict(fieldmask.FromPaths(tc.path), tc.req, atomic)
			require.ErrorIs(t, err, update.ErrBehaviorViolation)

			spec, err := update.NewSpec(&pb.Account{}, atomic)
			require.NoError(t, err)

			_, err = spec.Plan(tc.req, &fieldmaskpb.FieldMask{Paths: []string{tc.path}})
			require.ErrorIs(t, err, update.ErrBehaviorViolation)
		})
	}
}

// nodeTreeDescriptor builds Tree{repeated Node kids; map<string, Node> named},
// Node{repeated Node kids; string note}: collections of a recursive message.
func nodeTreeDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	field := func(name string, number int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(name), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}

	file := &descriptorpb.FileDescriptorProto{
		Name: new("nodetree.proto"), Package: new("nodetree"), Syntax: new("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: new("Tree"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("kids", 1, repeated, message, ".nodetree.Node"), field("named", 2, repeated, message, ".nodetree.Tree.NamedEntry"),
				},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    new("NamedEntry"),
					Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
					Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, optional, str, ""), field("value", 2, optional, message, ".nodetree.Node")},
				}},
			},
			{Name: new("Node"), Field: []*descriptorpb.FieldDescriptorProto{field("kids", 1, repeated, message, ".nodetree.Node"), field("note", 2, optional, str, "")}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Tree")
}

// TestWholeAtomicElementsDepth pins that an element of an atomic type named
// whole goes through the REQUIRED walk and its depth limit: a Node nested
// 100 levels deep under "kids.*" or "named.k", with Node atomic, is refused
// with fieldbehavior.ErrMaxDepthExceeded.
func TestWholeAtomicElementsDepth(t *testing.T) {
	t.Parallel()

	md := nodeTreeDescriptor(t)
	nodeMD := md.Fields().ByName("kids").Message()
	kids := nodeMD.Fields().ByName("kids")

	deepNode := func() *dynamicpb.Message {
		n := dynamicpb.NewMessage(nodeMD)
		for range 100 {
			parent := dynamicpb.NewMessage(nodeMD)
			parent.Mutable(kids).List().Append(protoreflect.ValueOfMessage(n))
			n = parent
		}

		return n
	}

	tree := dynamicpb.NewMessage(md)
	tree.Mutable(md.Fields().ByName("kids")).List().Append(protoreflect.ValueOfMessage(deepNode()))
	tree.Mutable(md.Fields().ByName("named")).Map().Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfMessage(deepNode()))

	atomic := atomicNames("nodetree.Node")

	for _, path := range []string{"kids.*", "named.k"} {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, proto.Clone(tree), atomic)
		require.ErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded, path)

		spec, err := update.NewSpec(dynamicpb.NewMessage(md), atomic)
		require.NoError(t, err)

		_, err = spec.Plan(tree, &fieldmaskpb.FieldMask{Paths: []string{path}})
		require.ErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded, path)
	}
}

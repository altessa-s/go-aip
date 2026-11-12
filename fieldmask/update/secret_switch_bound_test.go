// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// Several valid secret switches must not copy the unselected payload once per
// oneof. Measure bytes, not wall time: a byte slice is only one allocation.
func TestSecretSwitchesBoundedAllocations(t *testing.T) {
	root := &descriptorpb.DescriptorProto{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{{Name: new("payload"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()}}}

	child := &descriptorpb.DescriptorProto{Name: new("Child"), Field: []*descriptorpb.FieldDescriptorProto{{Name: new("immutable"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}}}
	for i := range 16 {
		root.OneofDecl = append(root.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: new(fmt.Sprintf("choice%d", i))})
		root.Field = append(root.Field,
			&descriptorpb.FieldDescriptorProto{Name: new(fmt.Sprintf("branch%d", i)), Number: new(int32(2 + i*2)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".secretbound.Child"), OneofIndex: new(int32(i))},
			&descriptorpb.FieldDescriptorProto{Name: new(fmt.Sprintf("secret%d", i)), Number: new(int32(3 + i*2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), OneofIndex: new(int32(i))})
	}

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: new("secretbound.proto"), Package: new("secretbound"), Syntax: new("proto3"), MessageType: []*descriptorpb.DescriptorProto{root, child}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	md := file.Messages().ByName("Root")
	req, stored := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	stored.Set(md.Fields().ByName("payload"), protoreflect.ValueOfBytes(bytes.Repeat([]byte{1}, 1<<20)))

	raw := &fieldmaskpb.FieldMask{}

	for i := range 16 {
		branch, secret := md.Fields().ByName(protoreflect.Name(fmt.Sprintf("branch%d", i))), md.Fields().ByName(protoreflect.Name(fmt.Sprintf("secret%d", i)))
		stored.Mutable(branch)
		req.Set(secret, protoreflect.ValueOfString("supplied"))
		raw.Paths = append(raw.Paths, string(secret.Name()))
	}

	resolver := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "immutable" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		if fd.Kind() == protoreflect.StringKind {
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
		}

		return nil
	}

	spec, err := update.NewSpec(req, update.WithResolver(fieldbehavior.ResolverFunc(resolver)))
	if err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	plan, err := spec.Plan(req, raw, update.WithCurrent(stored))

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatal(err)
	}

	if plan.Empty() {
		t.Fatal("secret inputs were lost")
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("16 secret switches allocated %d bytes", allocated)

	if allocated > 8<<20 {
		t.Fatalf("secret switches copied unselected payload: %d bytes", allocated)
	}
	// A populated protected descendant must still prevent switching the branch.
	branch := md.Fields().ByName("branch0")
	m := stored.Mutable(branch).Message()
	m.Set(m.Descriptor().Fields().ByName("immutable"), protoreflect.ValueOfString("stored"))

	if _, err := spec.Plan(req, raw, update.WithCurrent(stored)); err == nil {
		t.Fatal("switch cleared a populated immutable descendant")
	}
}

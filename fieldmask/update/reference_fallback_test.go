// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
)

func TestReferenceFailureKeepsEvaluatedRefusal(t *testing.T) {
	for _, failure := range []string{"panic", "error"} {
		t.Run(failure, func(t *testing.T) {
			req := (&descriptorpb.FileDescriptorProto{Name: new("new"), Package: new("new")}).ProtoReflect()
			stored := (&descriptorpb.FileDescriptorProto{Name: new("old"), Package: new("old")}).ProtoReflect()

			policy := newOptions(WithMaxPaths(1))
			if failure == "panic" {
				policy.resolver = fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					panic("reference-only failure")
				})
			}

			want := []FieldViolation{
				behavior.ProtectedViolation("name", annotations.FieldBehavior_IMMUTABLE),
				behavior.ProtectedViolation("package", annotations.FieldBehavior_IMMUTABLE),
			}

			u := &updateCheck{policy: policy, verify: true, orderRoot: msgPair{req: req, cur: stored},
				violationOrder: []fieldmask.FieldMask{fieldmask.FromPaths("name", "package")},
				violations:     []FieldViolation{want[1], want[0]}}
			if got, err := u.referenceViolations(req.Descriptor()); err != nil || len(got) != 0 {
				t.Fatalf("failed reference supplied diagnostics: %v", got)
			}

			if got, err := u.orderedViolations(req.Descriptor()); err != nil || !slices.Equal(got, want) {
				t.Fatalf("fallback changed evaluated refusal: got %v, want %v", got, want)
			}
		})
	}
}

func TestReferenceConversionFailureIsReturned(t *testing.T) {
	rebuilt, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	files := new(protoregistry.Files)
	if err := files.RegisterFile(rebuilt); err != nil {
		t.Fatal(err)
	}

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("reference_conversion.proto"), Package: new("reference"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/struct.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Resource"), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("payload"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.Struct")},
		}}},
	}, files)
	if err != nil {
		t.Fatal(err)
	}

	deep := structpb.NewStringValue("bottom")
	for range 10001 {
		deep = structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{deep}})
	}

	md := file.Messages().ByName("Resource")
	req, stored := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	// dynamicpb accepts a generated child of this full name; selecting it for
	// the reference walk must return its failed conversion, never equality.
	req.Set(md.Fields().ByName("payload"), protoreflect.ValueOfMessage((&structpb.Struct{
		Fields: map[string]*structpb.Value{"x": deep},
	}).ProtoReflect()))
	u := &updateCheck{policy: newOptions(WithImmutableFields("payload")), verify: true,
		orderRoot: msgPair{req: req, cur: stored}, violationOrder: []fieldmask.FieldMask{fieldmask.FromPaths("payload.x")},
		violations: []FieldViolation{behavior.ProtectedViolation("payload", annotations.FieldBehavior_IMMUTABLE)}}
	u.top = policyTopBehavior(u.policy)

	if _, err := u.orderedViolations(md); !errors.Is(err, ErrMessageMismatch) || !strings.Contains(err.Error(), "recursion") {
		t.Fatalf("reference conversion failure was hidden: %v", err)
	}
}

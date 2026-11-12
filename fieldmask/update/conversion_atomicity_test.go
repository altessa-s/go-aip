// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

func rebuiltStructFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()

	file, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	return file
}

// Build through public reflection: decoding this value would exceed protobuf's
// default wire recursion limit. Neither the mask nor the selected value is deep.
func overLimitStruct(md protoreflect.MessageDescriptor, x string) *dynamicpb.Message {
	fields := md.Fields().ByName("fields")
	valueMD := fields.MapValue().Message()
	value := dynamicpb.NewMessage(valueMD)
	value.Set(valueMD.Fields().ByName("string_value"), protoreflect.ValueOfString("bottom"))

	for range 10001 {
		parent := dynamicpb.NewMessage(valueMD)
		list := parent.Mutable(valueMD.Fields().ByName("list_value")).Message()
		list.Mutable(list.Descriptor().Fields().ByName("values")).List().Append(protoreflect.ValueOfMessage(value))
		value = parent
	}

	out := dynamicpb.NewMessage(md)
	entries := out.Mutable(fields).Map()
	entries.Set(protoreflect.ValueOfString("keep").MapKey(), protoreflect.ValueOfMessage(value))

	selected := dynamicpb.NewMessage(valueMD)
	selected.Set(valueMD.Fields().ByName("string_value"), protoreflect.ValueOfString(x))
	entries.Set(protoreflect.ValueOfString("x").MapKey(), protoreflect.ValueOfMessage(selected))

	return out
}

func TestReconstructedDeepUnselectedValuePreserved(t *testing.T) {
	md := rebuiltStructFile(t).Messages().ByName("Struct")

	for _, entry := range []string{"MergePaths", "Merge", "ApplyMask", "Complete"} {
		t.Run(entry, func(t *testing.T) {
			stored := overLimitStruct(md, "old")
			request := dynamicpb.NewMessage(md)
			fields := md.Fields().ByName("fields")
			value := dynamicpb.NewMessage(fields.MapValue().Message())
			value.Set(value.Descriptor().Fields().ByName("string_value"), protoreflect.ValueOfString("new"))
			request.Mutable(fields).Map().Set(protoreflect.ValueOfString("x").MapKey(), protoreflect.ValueOfMessage(value))

			before := proto.Clone(stored)
			want := proto.Clone(stored)
			want.ProtoReflect().Mutable(fields).Map().Set(protoreflect.ValueOfString("x").MapKey(), protoreflect.ValueOfMessage(value))

			mask := &fieldmaskpb.FieldMask{Paths: []string{"x"}}

			var err error

			switch entry {
			case "MergePaths":
				err = update.MergePaths(stored, request, mask.Paths)
			case "Merge":
				err = update.Merge(fieldmask.FromPaths(mask.Paths...), stored, request)
			case "ApplyMask", "Complete":
				var effective fieldmask.FieldMask

				if entry == "ApplyMask" {
					effective, err = update.ApplyMask(mask, request, update.WithCurrent(stored))
				} else {
					var pending *update.DeferredPlan

					pending, err = update.Prepare(mask, request)
					if err == nil {
						effective, err = pending.Complete(stored)
					}
				}

				if !proto.Equal(before, stored) {
					t.Fatal("normalization mutated stored state")
				}

				if err == nil {
					err = update.Merge(effective, stored, request)
				}
			}

			if err != nil {
				t.Fatal(err)
			}

			if !proto.Equal(want, stored) {
				t.Fatal("merge changed the unselected keep subtree or lost the selected x value")
			}
		})
	}
}

// conversionFault asks a destination allocation to use another descriptor for
// the deep Struct at exactly one copy. This exercises the actual wire fallback
// and recursion error, without a production hook or a synthetic error return.
type conversionFault struct {
	calls, failAt int
	descriptor    protoreflect.MessageDescriptor
}

type conversionMessage struct {
	protoreflect.Message
	fault *conversionFault
}

func (m *conversionMessage) ProtoReflect() protoreflect.Message   { return m }
func (m *conversionMessage) Interface() protoreflect.ProtoMessage { return m }
func (m *conversionMessage) New() protoreflect.Message {
	return &conversionMessage{Message: m.Message.New(), fault: m.fault}
}

func (m *conversionMessage) NewField(fd protoreflect.FieldDescriptor) protoreflect.Value {
	if fd.Name() == "payload" {
		m.fault.calls++
		if m.fault.calls == m.fault.failAt {
			return protoreflect.ValueOfMessage(dynamicpb.NewMessage(m.fault.descriptor))
		}
	}

	return m.Message.NewField(fd)
}

func conversionFixture(t *testing.T) (proto.Message, proto.Message, *conversionFault) {
	t.Helper()

	output := &descriptorpb.FieldOptions{}
	proto.SetExtension(output, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY})

	input := &descriptorpb.FieldOptions{}
	proto.SetExtension(input, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY})

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("conversion_atomicity.proto"), Package: new("conversion"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/struct.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Resource"), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("note"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("discard"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Options: output},
			{Name: new("payload"), Number: new(int32(3)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.Struct")},
			{Name: new("secret"), Number: new(int32(4)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Options: input},
		}}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	md := file.Messages().ByName("Resource")
	fault := &conversionFault{descriptor: rebuiltStructFile(t).Messages().ByName("Struct")}
	makeResource := func(note string) proto.Message {
		m := &conversionMessage{Message: dynamicpb.NewMessage(md), fault: fault}
		m.Set(md.Fields().ByName("note"), protoreflect.ValueOfString(note))
		m.Set(md.Fields().ByName("discard"), protoreflect.ValueOfString("server"))
		m.Set(md.Fields().ByName("secret"), protoreflect.ValueOfString("write-only"))
		// Reflection preserves this value; wire conversion to the rebuilt
		// descriptor fails without exceeding the update traversal limit.
		payload := &structpb.Struct{Fields: map[string]*structpb.Value{"x": structpb.NewStringValue("\xff")}}
		m.Set(md.Fields().ByName("payload"), protoreflect.ValueOfMessage(payload.ProtoReflect()))

		return m
	}

	return makeResource("new"), makeResource("old"), fault
}

func TestMutatingEntryPointsLastConversionIsAtomic(t *testing.T) {
	for _, entry := range []string{
		"MergePaths", "Merge", "MergeAll", "ApplyMask", "Prepare", "Apply", "Complete", "CompleteStrict", "KeepIdentity",
		"Spec", "BoundSpec", "Patch", "BoundPatch", "Strip", "StripCreate", "StripUpdate", "StripResponse",
	} {
		t.Run(entry, func(t *testing.T) {
			run := func(failAt int) int {
				req, stored, fault := conversionFixture(t)
				raw := &fieldmaskpb.FieldMask{Paths: []string{"note", "payload"}}
				selection := fieldmask.FromPaths(raw.Paths...)

				spec, err := update.NewSpec(req)
				if err != nil {
					t.Fatal(err)
				}

				ctx := t.Context()

				var pending *update.DeferredPlan

				if strings.HasPrefix(entry, "Complete") || strings.HasPrefix(entry, "Bound") {
					pending, err = update.Prepare(raw, req, update.WithStrict(entry == "CompleteStrict"))
					if err != nil {
						t.Fatal(err)
					}

					ctx = update.ContextWithPlan(ctx, pending)
				}

				beforeRequest, beforeStored := proto.Clone(req), proto.Clone(stored)
				beforeMask := selection.Clone()
				fault.calls, fault.failAt = 0, failAt
				writes := 0
				patch := update.Patch[proto.Message, proto.Message, *int64]{
					Spec: spec, Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
					Read:    func(context.Context) (proto.Message, error) { return stored, nil },
					ToProto: func(m proto.Message) proto.Message { return m },
					Save: func(_ context.Context, merged proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
						writes++
						return merged, nil
					},
				}

				switch entry {
				case "MergePaths":
					err = update.MergePaths(stored, req, raw.Paths)
				case "Merge":
					err = update.Merge(selection, stored, req)
				case "MergeAll":
					err = update.Merge(fieldmask.FromPaths("*"), stored, req)
				case "ApplyMask":
					_, err = update.ApplyMask(raw, req, update.WithCurrent(stored))
				case "Prepare":
					_, err = update.Prepare(raw, req, update.WithCurrent(stored))
				case "Apply":
					err = update.Apply(selection, req, update.WithCurrent(stored))
				case "Complete", "CompleteStrict":
					_, err = pending.Complete(stored)
				case "KeepIdentity":
					err = update.KeepIdentity(req, update.WithIdentityFields("payload"), update.WithCurrent(stored))
				case "Spec", "BoundSpec":
					_, err = spec.Plan(req, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
				case "Patch", "BoundPatch":
					_, err = patch.Run(ctx, req, "", raw)
				case "StripUpdate":
					err = fieldbehavior.StripUpdate(req, fieldbehavior.WithCurrent(stored))
				case "Strip":
					err = fieldbehavior.Strip(req, fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY))
				case "StripCreate":
					err = fieldbehavior.StripCreate(req)
				case "StripResponse":
					err = fieldbehavior.StripResponse(req)
				}

				if failAt == 0 {
					if err != nil || fault.calls == 0 {
						t.Fatalf("baseline failed or never copied payload: %v, copies=%d", err, fault.calls)
					}

					return fault.calls
				}

				want := update.ErrMessageMismatch
				if strings.HasPrefix(entry, "Strip") {
					want = fieldbehavior.ErrCurrentMismatch
				}

				if !errors.Is(err, want) || !strings.Contains(err.Error(), "UTF-8") || fault.calls != failAt {
					t.Fatalf("last conversion did not return the wire encoding error: %v, copies=%d, failAt=%d", err, fault.calls, failAt)
				}

				if !proto.Equal(beforeRequest, req) || !proto.Equal(beforeStored, stored) || !reflect.DeepEqual(beforeMask, selection) {
					t.Fatal("failed conversion published a partial request, destination, or mask")
				}

				if writes != 0 {
					t.Fatal("conversion failure reached Save")
				}

				return fault.calls
			}

			last := run(0)
			run(last)
		})
	}
}

// The stored descriptor mismatch fails after KeepIdentity has cleared note on
// its working request. No injected allocator is needed to reproduce this case.
func TestKeepIdentityStoredConversionFailureIsAtomic(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("identity_conversion.proto"), Package: new("identityconversion"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/struct.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Resource"), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("id"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("note"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("payload"), Number: new(int32(3)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.Struct")},
		}}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	md := file.Messages().ByName("Resource")
	request, stored := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	request.Set(md.Fields().ByName("id"), protoreflect.ValueOfString("r"))
	request.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("retain-on-error"))
	stored.Set(md.Fields().ByName("id"), protoreflect.ValueOfString("r"))
	stored.Set(md.Fields().ByName("payload"), protoreflect.ValueOfMessage(overLimitStruct(rebuiltStructFile(t).Messages().ByName("Struct"), "old")))

	beforeRequest := proto.Clone(request)
	// Clone the child in its own descriptor; proto.Clone of the mixed root
	// assumes descriptor identity and cannot make this snapshot safely.
	beforeStored := dynamicpb.NewMessage(md)
	beforeStored.Set(md.Fields().ByName("id"), stored.Get(md.Fields().ByName("id")))
	payload := stored.Get(md.Fields().ByName("payload")).Message()
	beforeStored.Set(md.Fields().ByName("payload"), protoreflect.ValueOfMessage(proto.Clone(payload.Interface()).ProtoReflect()))

	err = update.KeepIdentity(request, update.WithCurrent(stored))
	if !errors.Is(err, update.ErrMessageMismatch) || !strings.Contains(err.Error(), "recursion") {
		t.Fatalf("want stored payload conversion error, got %v", err)
	}

	if !proto.Equal(request, beforeRequest) || !proto.Equal(stored, beforeStored) {
		t.Fatal("stored conversion failure changed request or stored state")
	}
}

func TestPatchCallbackFailureDoesNotPublish(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "bound"}[bound], func(t *testing.T) {
			request := &descriptorpb.FileDescriptorProto{Name: new("secret"), Package: new("new")}
			stored := &descriptorpb.FileDescriptorProto{Package: new("old")}
			policy := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
				if fd.Name() == "name" {
					return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
				}

				return nil
			}))

			spec, err := update.NewSpec(request, policy)
			if err != nil {
				t.Fatal(err)
			}

			raw := &fieldmaskpb.FieldMask{Paths: []string{"package"}}
			ctx := t.Context()

			if bound {
				pending, err := update.Prepare(raw, request, policy)
				if err != nil {
					t.Fatal(err)
				}

				ctx = update.ContextWithPlan(ctx, pending)
				request.Package = new("after normalization")
			}

			beforeRequest, beforeStored := proto.Clone(request), proto.Clone(stored)
			failure := errors.New("save failed")
			called := false
			patch := update.Patch[*descriptorpb.FileDescriptorProto, *descriptorpb.FileDescriptorProto, *int64]{
				Spec: spec,
				Precondition: update.Int64Precondition[*descriptorpb.FileDescriptorProto]{
					Version: func(*descriptorpb.FileDescriptorProto) int64 { return 1 },
				},
				Read:    func(context.Context) (*descriptorpb.FileDescriptorProto, error) { return stored, nil },
				ToProto: func(m *descriptorpb.FileDescriptorProto) *descriptorpb.FileDescriptorProto { return m },
				Save: func(_ context.Context, merged *descriptorpb.FileDescriptorProto, plan update.Plan, _ *int64) (*descriptorpb.FileDescriptorProto, error) {
					called = true

					secret := plan.Secret("name", request.Name)
					if secret == nil || *secret != "secret" || merged.GetPackage() != "new" {
						t.Fatal("Save did not receive the validated private input")
					}

					if !proto.Equal(request, beforeRequest) || !proto.Equal(stored, beforeStored) {
						t.Fatal("library published request or stored state before Save")
					}

					merged.Package = new("callback mutation")

					return nil, failure
				},
			}

			_, err = patch.Run(ctx, request, "", raw)
			if !called || !errors.Is(err, failure) || !proto.Equal(request, beforeRequest) || !proto.Equal(stored, beforeStored) {
				t.Fatalf("Save failure published partial state: %v", err)
			}
		})
	}
}

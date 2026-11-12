// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestProtectedWildcardIgnoredKey(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, enclosing := range []bool{false, true} {
			for _, policy := range []bool{false, true} {
				if policy && b == annotations.FieldBehavior_IDENTIFIER {
					continue // Spec reserves policy-only identity fields for routing.
				}

				for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "DeferredSpec", "Patch", "DeferredPatch"} {
					t.Run(fmt.Sprintf("%s/enclosing=%t/policy=%t/%s", b, enclosing, policy, entry), func(t *testing.T) {
						req := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e", Note: "new", CreateTime: "client"}}}
						stored := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e", Note: "old", CreateTime: "server"}}}
						paths, protected := []string{"delegates.*.note", "delegates.k.create_time"}, "delegates"

						want := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e", Note: "new"}}}
						if enclosing {
							req, stored, want = &pb.Account{Parent: req}, &pb.Account{Parent: stored}, &pb.Account{Parent: want}
							paths, protected = []string{"parent.delegates.*.note", "parent.delegates.k.create_time"}, "parent"
						}

						if entry == "ValidateStrict" || entry == "Spec" || entry == "Patch" {
							want = proto.CloneOf(req) // These entry points do not normalize the caller's request.
						}

						opt := protectedOption(protected, b, policy)
						before := proto.Clone(stored)

						mask, err := round2Entry(t, entry, req, stored, maskOf(paths), opt)
						if err != nil || len(mask) != 0 {
							t.Fatalf("ignored keyed selection must succeed with empty mask: mask=%v err=%v", mask, err)
						}

						requireProtoEqual(t, want, req)

						merged := proto.Clone(stored)
						if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, before, merged)
						requireProtoEqual(t, before, stored)
					})
				}
			}
		}
	}
}

func protectedOption(name string, b annotations.FieldBehavior, policy bool) update.Option {
	if policy {
		if b == annotations.FieldBehavior_IDENTIFIER {
			return update.WithIdentityFields(name)
		}

		return update.WithImmutableFields(name)
	}

	return update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if string(fd.Name()) == name {
			return []annotations.FieldBehavior{b}
		}

		return fieldbehavior.AnnotationResolver{}.Resolve(fd)
	}))
}

// messageChoiceDescriptor builds Root{Config config}, Config{oneof choice
// {Child a; string b}; Child child}, Child{string note; string stamp}.
func messageChoiceDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	field := func(name string, number int32, child string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}
		if child != "" {
			fd.Type, fd.TypeName = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), new(".messagechoice."+child)
		}

		return fd
	}
	a, b := field("a", 1, "Child"), field("b", 2, "")
	a.OneofIndex, b.OneofIndex = new(int32(0)), new(int32(0))
	stamp := field("stamp", 2, "")
	stamp.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(stamp.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY})

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("messagechoice.proto"), Package: new("messagechoice"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), Field: []*descriptorpb.FieldDescriptorProto{field("config", 1, "Config")}},
			{Name: new("Config"), OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("choice")}},
				Field: []*descriptorpb.FieldDescriptorProto{a, b, field("child", 3, "Child")}},
			{Name: new("Child"), Field: []*descriptorpb.FieldDescriptorProto{field("note", 1, ""), stamp}},
		},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	return file.Messages().ByName("Root")
}

func parseMessageChoice(t *testing.T, md protoreflect.MessageDescriptor, text string) proto.Message {
	t.Helper()

	m := dynamicpb.NewMessage(md)
	if err := prototext.Unmarshal([]byte(text), m); err != nil {
		t.Fatal(err)
	}

	return m
}

func TestProtectedMessageOneofEffect(t *testing.T) {
	md := messageChoiceDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, policy := range []bool{false, true} {
			if policy && b == annotations.FieldBehavior_IDENTIFIER {
				continue // Exercise IDENTIFIER through its behavior resolver.
			}

			for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "DeferredSpec", "Patch", "DeferredPatch"} {
				t.Run(fmt.Sprintf("%s/policy=%t/%s", b, policy, entry), func(t *testing.T) {
					req := parseMessageChoice(t, md, `config { a {} }`)
					stored := parseMessageChoice(t, md, `config { b: "stored" }`)
					before := proto.Clone(req)
					paths := []string{"config.a.note"}

					mutated := proto.Clone(stored)
					if err := update.MergePaths(mutated, req, paths); err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, parseMessageChoice(t, md, `config { a {} }`), mutated)

					_, err := round2Entry(t, entry, req, stored, maskOf(paths), protectedOption("config", b, policy))
					if err == nil {
						t.Fatal("accepted oneof transition clearing protected config.b")
					}

					switch entry {
					case "Spec", "DeferredSpec", "Patch", "DeferredPatch":
						if !errors.Is(err, update.ErrProtectedField) {
							t.Fatalf("want protected-field error, got %v", err)
						}
					default:
						var violations []update.FieldViolation

						if be, ok := errors.AsType[*update.BehaviorError](err); ok {
							violations = be.Violations
						}

						if se, ok := errors.AsType[*update.StrictError](err); ok {
							violations = se.Violations
						}

						if len(violations) != 1 || violations[0].Path != "config" || violations[0].Behavior != b {
							t.Fatalf("want %s config, got %v", b, err)
						}
					}

					requireProtoEqual(t, before, req)
					requireProtoEqual(t, parseMessageChoice(t, md, `config { b: "stored" }`), stored)
				})
			}
		}
	}
}

func TestEmptyEffectiveMaskStillNeedsCompletion(t *testing.T) {
	req := &pb.Account{Members: []*pb.Owner{{Email: "new", Org: "same", CreateTime: "client"}}}
	stored := &pb.Account{Members: []*pb.Owner{{Email: "old", Org: "same"}}}

	pending, err := update.Prepare(maskOf([]string{"members"}), req)
	if err != nil || len(pending.Mask()) != 0 {
		t.Fatalf("want empty interim mask: plan=%v err=%v", pending, err)
	}

	requireProtoEqual(t, &pb.Account{Members: []*pb.Owner{{Email: "new", Org: "same"}}}, req)

	mask, err := pending.Complete(stored)
	if err != nil || !slices.Equal(mask.ToPaths(), []string{"members"}) {
		t.Fatalf("want completed members replacement: mask=%v err=%v", mask, err)
	}

	merged := proto.Clone(stored)
	if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, &pb.Account{Members: []*pb.Owner{{Email: "new", Org: "same"}}}, merged)

	req = &pb.Account{UpdateTime: "client"}

	mask, err = update.ApplyMask(maskOf([]string{"update_time"}), req)
	if err != nil || len(mask) != 0 {
		t.Fatalf("want completed empty mask: mask=%v err=%v", mask, err)
	}

	requireProtoEqual(t, &pb.Account{}, req)
}

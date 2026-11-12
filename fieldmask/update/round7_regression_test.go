// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	jsonpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// The empty mutable branch must have no OUTPUT_ONLY descendants: those hid the
// difference between the old mask synthesis and the planner's leaf selection.
func plainMessageChoiceDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	file := protodesc.ToFileDescriptorProto(messageChoiceDescriptor(t).ParentFile())
	file.MessageType[2].Field = file.MessageType[2].Field[:1]

	md, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	return md.Messages().ByName("Root")
}

func TestRound7EmptyImpliedOneofMessage(t *testing.T) {
	md := plainMessageChoiceDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "b" {
				return []annotations.FieldBehavior{b}
			}

			return nil
		})

		for _, entry := range round6Entries {
			t.Run(fmt.Sprintf("%s/%s", b, entry), func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				stored := parseMessageChoice(t, md, `config { b: "stored" }`)
				before := proto.Clone(stored)

				mask, err := round2Entry(t, entry, req, stored, nil, update.WithResolver(resolver))
				if err != nil || len(mask) != 0 {
					t.Fatalf("empty mutable message implies no writes: mask=%v err=%v", mask, err)
				}

				requireProtoEqual(t, before, stored)
			})
		}

		for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Prepare", "Spec"} {
			t.Run(fmt.Sprintf("%s/stateless/%s", b, entry), func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				opts := []update.Option{update.WithResolver(resolver)}

				var mask fieldmask.FieldMask

				var err error

				switch entry {
				case "ApplyMask":
					mask, err = update.ApplyMask(nil, req, opts...)
				case "ApplyMaskStrict":
					mask, err = update.ApplyMask(nil, req, append(opts, update.WithStrict(true))...)
				case "Apply":
					err = update.Apply(nil, req, opts...)
				case "ValidateStrict":
					mask, err = update.ValidateStrict(nil, req, opts...)
				case "Prepare":
					var pending *update.DeferredPlan

					pending, err = update.Prepare(nil, req, opts...)
					if err == nil {
						mask = pending.Mask()
					}
				case "Spec":
					spec, specErr := update.NewSpec(req, opts...)
					if specErr != nil {
						t.Fatal(specErr)
					}

					var plan update.Plan

					plan, err = spec.Plan(req, nil)
					mask = fieldmask.FromPaths(plan.Paths...)
				}

				if err != nil || len(mask) != 0 {
					t.Fatalf("empty mutable message implies no writes: mask=%v err=%v", mask, err)
				}

				requireProtoEqual(t, parseMessageChoice(t, md, `config { a {} }`), req)
			})
		}

		for _, known := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stateless=%t", b, !known), func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				stored := parseMessageChoice(t, md, `config { b: "stored" }`)
				opts := []update.Option{update.WithResolver(resolver)}
				stripOpts := []fieldbehavior.Option{fieldbehavior.WithResolver(resolver)}

				if known {
					opts = append(opts, update.WithCurrent(stored))
					stripOpts = append(stripOpts, fieldbehavior.WithCurrent(stored))
				}

				if err := fieldbehavior.StripUpdate(req, stripOpts...); err != nil {
					t.Fatal(err)
				}

				mask, err := update.ApplyMask(nil, req, opts...)
				if err != nil || len(mask) != 0 {
					t.Fatalf("empty mutable message implies no writes: mask=%v err=%v", mask, err)
				}
			})
		}

		t.Run(b.String()+"/PatchDoesNotSave", func(t *testing.T) {
			req := parseMessageChoice(t, md, `config { a {} }`)
			stored := parseMessageChoice(t, md, `config { b: "stored" }`)

			spec, err := update.NewSpec(req, update.WithResolver(resolver))
			if err != nil {
				t.Fatal(err)
			}

			patch := update.Patch[proto.Message, proto.Message, *int64]{
				Field: "resource", Spec: spec,
				Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
				Read:         func(context.Context) (proto.Message, error) { return proto.Clone(stored), nil },
				ToProto:      func(m proto.Message) proto.Message { return m },
				Save: func(context.Context, proto.Message, update.Plan, *int64) (proto.Message, error) {
					t.Fatal("empty implied plan must not save")
					return nil, nil
				},
			}

			got, err := patch.Run(t.Context(), req, "", nil)
			if err != nil {
				t.Fatal(err)
			}

			requireProtoEqual(t, stored, got)
		})
	}
}

func TestRound7ExplicitOneofClearStillRejected(t *testing.T) {
	md := plainMessageChoiceDescriptor(t)
	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "b" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	})

	for _, path := range []string{"config.a.note", "config.a", "config", "*"} {
		for _, entry := range round6Entries {
			t.Run(path+"/"+entry, func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				stored := parseMessageChoice(t, md, `config { b: "stored" }`)

				_, err := round2Entry(t, entry, req, stored, maskOf([]string{path}), update.WithResolver(resolver))
				if (path == "config.a.note" || path == "config.a") && (entry == "Complete" || entry == "DeferredSpec" || entry == "DeferredPatch") {
					// A purely mutable explicit selection retains the existing
					// conservative oneof refusal during stateless preparation.
					if !errors.Is(err, update.ErrBehaviorViolation) || !strings.Contains(err.Error(), `would clear "b"`) {
						t.Fatalf("want oneof-clear rejection during preparation, got %v", err)
					}

					return
				}

				requireProtectedViolation(t, err, "config.b", annotations.FieldBehavior_IMMUTABLE)
			})
		}
	}
}

func TestRound7PopulatedProtectedMessageStillCompared(t *testing.T) {
	md := plainMessageChoiceDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "a" {
				return []annotations.FieldBehavior{b}
			}

			return nil
		})

		for _, entry := range round6Entries {
			t.Run(b.String()+"/"+entry, func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				stored := parseMessageChoice(t, md, `config {}`)
				_, err := round2Entry(t, entry, req, stored, nil, update.WithResolver(resolver))
				requireProtectedViolation(t, err, "config.a", b)
			})
		}
	}
}

func TestRound7EntirelyOutputOnlyModes(t *testing.T) {
	resolver := fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
	})
	stored := &pb.Account{Members: []*pb.Owner{{CreateTime: "server"}}, Delegates: map[string]*pb.Owner{"k": {CreateTime: "server"}}}
	request := &pb.Account{Members: []*pb.Owner{{CreateTime: "client"}, {CreateTime: "extra"}},
		Delegates: map[string]*pb.Owner{"k": {CreateTime: "client"}, "extra": {CreateTime: "extra"}}}

	for _, paths := range [][]string{nil, {"*"}, {"members.*.create_time", "delegates.*.create_time"}} {
		for _, entry := range round6Entries {
			t.Run(fmt.Sprintf("%v/%s", paths, entry), func(t *testing.T) {
				mask, err := round2Entry(t, entry, proto.Clone(request), stored, maskOf(paths),
					update.WithResolver(resolver), update.WithImmutableFields("members", "delegates"))
				if err != nil || len(mask) != 0 {
					t.Fatalf("entirely OUTPUT_ONLY update must be ignored: mask=%v err=%v", mask, err)
				}
			})
		}
	}
}

func TestRound7ImpliedDynamicValues(t *testing.T) {
	value := structpb.NewStringValue("new")
	req := &jsonpb.UpdateRequest{
		ExtraData: &structpb.Struct{Fields: map[string]*structpb.Value{"key": value}},
		ValueData: value,
		ListData:  &structpb.ListValue{Values: []*structpb.Value{value}},
	}
	want := []string{"extra_data", "list_data", "value_data"}

	spec, err := update.NewSpec(req)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := spec.Plan(proto.Clone(req), nil, update.WithCurrent(&jsonpb.UpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	mask, err := update.ApplyMask(nil, proto.Clone(req), update.WithCurrent(&jsonpb.UpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	for _, paths := range [][]string{plan.Paths, mask.ToPaths(), update.FromSetFields(req).ToPaths()} {
		if !slices.Equal(paths, want) {
			t.Fatalf("populated dynamic values must remain selected: got=%v want=%v", paths, want)
		}
	}

	checkDeferredEquivalence(t, nil, req, &jsonpb.UpdateRequest{})
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestRound5StripOneofClear(t *testing.T) {
	md := messageChoiceDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, tc := range []struct{ name, stored, request, protected string }{
			{"scalar", `config { b: "old" }`, `config { a { note: "new" } }`, "b"},
			{"message", `config { a { note: "old" } }`, `config { b: "new" }`, "a"},
			{"descendant", `config { a { note: "old" } }`, `config { b: "new" }`, "note"},
		} {
			t.Run(fmt.Sprintf("%s/%s", b, tc.name), func(t *testing.T) {
				req, stored := parseMessageChoice(t, md, tc.request), parseMessageChoice(t, md, tc.stored)
				before := proto.Clone(req)
				resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					if string(fd.Name()) == tc.protected {
						return []annotations.FieldBehavior{b}
					}

					return fieldbehavior.AnnotationResolver{}.Resolve(fd)
				})
				err := fieldbehavior.StripUpdate(req, fieldbehavior.WithCurrent(stored), fieldbehavior.WithResolver(resolver))
				be, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)

				path := "config." + tc.protected
				if tc.protected == "note" {
					path = "config.a.note"
				}

				if !ok || len(be.Violations) != 1 || be.Violations[0].Path != path || be.Violations[0].Behavior != b {
					t.Fatalf("want %s violation at %s, got %v", b, path, err)
				}

				requireProtoEqual(t, before, req)
				requireProtoEqual(t, parseMessageChoice(t, md, tc.stored), stored)
			})
		}
	}
}

func TestRound5ProtectedTrailingWildcard(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, path := range []string{"parent.emails.*", "parent.attributes.*"} {
			for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "DeferredSpec", "Patch", "DeferredPatch"} {
				t.Run(fmt.Sprintf("%s/%s/%s", b, path, entry), func(t *testing.T) {
					stored := &pb.Account{Parent: &pb.Account{Emails: []string{"old"}, Attributes: map[string]string{"k": "old"}}}
					req := &pb.Account{Parent: &pb.Account{Emails: []string{"new"}, Attributes: map[string]string{"k": "new"}}}

					_, err := round2Entry(t, entry, req, stored, maskOf([]string{path}), protectedOption("parent", b, b == annotations.FieldBehavior_IMMUTABLE))
					if err == nil {
						t.Fatal("accepted protected collection replacement")
					}

					if entry == "Spec" || entry == "DeferredSpec" || entry == "Patch" || entry == "DeferredPatch" {
						if !errors.Is(err, update.ErrProtectedField) {
							t.Fatalf("want protected field error: %v", err)
						}

						return
					}

					var violations []update.FieldViolation
					if be, ok := errors.AsType[*update.BehaviorError](err); ok {
						violations = be.Violations
					}

					if se, ok := errors.AsType[*update.StrictError](err); ok {
						violations = se.Violations
					}

					if len(violations) != 1 || violations[0].Path != "parent" || violations[0].Behavior != b {
						t.Fatalf("want %s parent: %v", b, err)
					}
				})
			}
		}
	}
}

func TestRound5StripAtomicPipeline(t *testing.T) {
	for _, stateful := range []bool{false, true} {
		for _, patch := range []bool{false, true} {
			t.Run(fmt.Sprintf("current=%t/patch=%t", stateful, patch), func(t *testing.T) {
				stored := &pb.Account{Owner: &pb.Owner{Email: "e", Org: "same", Note: "old"}}
				req := &pb.Account{Owner: &pb.Owner{Email: "e", Org: "same", Note: "new"}}

				spec, err := update.NewSpec(req, atomicNames("fieldbehaviortest.v1.Owner"))
				if err != nil {
					t.Fatal(err)
				}

				if _, err := spec.Plan(proto.Clone(req), nil, update.WithCurrent(stored)); err != nil {
					t.Fatalf("original request: %v", err)
				}

				var opts []fieldbehavior.Option
				if stateful {
					opts = append(opts, fieldbehavior.WithCurrent(stored))
				}

				if err := fieldbehavior.StripUpdate(req, opts...); err != nil {
					t.Fatal(err)
				}

				var got proto.Message

				if patch {
					store := &accountStore{cur: proto.CloneOf(stored)}
					p := store.patch(t)
					p.Spec = spec
					got, err = p.Run(t.Context(), req, "", nil)
				} else {
					var plan update.Plan

					plan, err = spec.Plan(req, nil, update.WithCurrent(stored))
					if err == nil {
						got = proto.Clone(stored)
						err = update.MergePaths(got, req, plan.Paths, atomicNames("fieldbehaviortest.v1.Owner"))
					}
				}

				if err != nil {
					t.Fatalf("stripped request: %v", err)
				}

				requireProtoEqual(t, &pb.Account{Owner: &pb.Owner{Email: "e", Org: "same", Note: "new"}}, got)
			})
		}
	}
}

// Descriptor-compatible values are converted by the shared merge writer.
func TestRound5StripMixedRepresentation(t *testing.T) {
	stored := &pb.Account{Owner: &pb.Owner{Org: "same"}}

	req := dynamicpb.NewMessage(stored.ProtoReflect().Descriptor())
	if err := fieldbehavior.StripUpdate(req, fieldbehavior.WithCurrent(stored)); err != nil {
		t.Fatalf("mixed representation: %v", err)
	}
}

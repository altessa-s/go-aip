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
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	jsonpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

var round6Entries = []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "DeferredSpec", "Patch", "DeferredPatch"}

func TestRound6ProtectedLeafOneofSibling(t *testing.T) {
	md := messageChoiceDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "b" || fd.Name() == "note" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, entry := range round6Entries {
			t.Run(fmt.Sprintf("%s/%s", b, entry), func(t *testing.T) {
				req := parseMessageChoice(t, md, `config { a {} }`)
				stored := parseMessageChoice(t, md, `config { b: "stored" }`)
				before := proto.Clone(stored)
				paths := []string{"config.a.note"}
				merged := proto.Clone(stored)

				if err := update.MergePaths(merged, req, paths); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, req, merged)

				_, err := round2Entry(t, entry, req, stored, maskOf(paths), update.WithResolver(resolver))
				requireProtectedViolation(t, err, "config.b", b)
				requireProtoEqual(t, before, stored)

				wantRequest := `config { a {} }`
				if entry == "DeferredSpec" || entry == "DeferredPatch" {
					wantRequest = "" // refusal preserves the sanitized boundary request
				}

				requireProtoEqual(t, parseMessageChoice(t, md, wantRequest), req)
			})
		}
	}
}

func TestRound6ProtectedElementCounts(t *testing.T) {
	for _, enclosing := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			for _, entry := range round6Entries {
				t.Run(fmt.Sprintf("enclosing=%t/reverse=%t/%s", enclosing, reverse, entry), func(t *testing.T) {
					stored := &pb.Account{Members: []*pb.Owner{{Note: "same"}}}
					req := &pb.Account{Members: []*pb.Owner{{Note: "same"}, {Note: "extra"}}}
					path, protected := "members.*.note", "members"

					if enclosing {
						req, stored = &pb.Account{Parent: req}, &pb.Account{Parent: stored}
						path, protected = "parent.members.*.note", "parent"
					}

					if reverse {
						req, stored = stored, req
					}

					before := proto.Clone(stored)
					if err := update.MergePaths(proto.Clone(stored), req, []string{path}); !errors.Is(err, update.ErrElementCountMismatch) {
						t.Fatalf("oracle: want element count mismatch, got %v", err)
					}

					_, err := round2Entry(t, entry, req, stored, maskOf([]string{path}), update.WithImmutableFields(protected))
					if !errors.Is(err, update.ErrElementCountMismatch) {
						t.Fatalf("want element count mismatch, got %v", err)
					}

					requireProtoEqual(t, before, stored)
				})
			}
		}
	}
}

func requireProtectedViolation(t *testing.T, err error, path string, b annotations.FieldBehavior) {
	t.Helper()

	var violations []update.FieldViolation

	if be, ok := errors.AsType[*update.BehaviorError](err); ok {
		violations = be.Violations
	}

	if se, ok := errors.AsType[*update.StrictError](err); ok {
		violations = se.Violations
	}

	for _, v := range violations {
		if v.Path == path && v.Behavior == b {
			return
		}
	}

	if mp, ok := errors.AsType[*update.MaskPathError](err); ok && errors.Is(err, update.ErrProtectedField) && mp.Field == path {
		return
	}

	t.Fatalf("want %s violation naming %s, got %v", b, path, err)
}

func TestRound6IgnoredCollectionIgnoresCounts(t *testing.T) {
	for _, equal := range []bool{false, true} {
		for _, entry := range round6Entries {
			t.Run(fmt.Sprintf("equal=%t/%s", equal, entry), func(t *testing.T) {
				req := &pb.Account{Members: []*pb.Owner{{CreateTime: "client"}}}

				stored := &pb.Account{Members: []*pb.Owner{{CreateTime: "server"}}}
				if !equal {
					req.Members = append(req.Members, &pb.Owner{CreateTime: "extra"})
				}

				mask, err := round2Entry(t, entry, req, stored, maskOf([]string{"members.*.create_time"}), update.WithImmutableFields("members"))
				if err != nil || len(mask) != 0 {
					t.Fatalf("OUTPUT_ONLY selection must be ignored regardless of counts: mask=%v err=%v", mask, err)
				}
			})
		}
	}
}

func TestRound6DeferredDynamicCounts(t *testing.T) {
	value := func(count int) *jsonpb.UpdateRequest {
		items := make([]any, count)
		for i := range items {
			items[i] = map[string]any{"name": "same"}
		}

		data, err := structpb.NewStruct(map[string]any{"items": items})
		if err != nil {
			t.Fatal(err)
		}

		return &jsonpb.UpdateRequest{ExtraData: data}
	}

	for _, equal := range []bool{false, true} {
		for _, entry := range round6Entries {
			t.Run(fmt.Sprintf("equal=%t/%s", equal, entry), func(t *testing.T) {
				req, stored := value(2), value(1)
				if equal {
					stored = value(2)
				}

				_, err := round2Entry(t, entry, req, stored, maskOf([]string{"extra_data.items.*.name"}))
				if equal && err != nil {
					t.Fatalf("valid dynamic counts: %v", err)
				}

				if !equal && !errors.Is(err, update.ErrElementCountMismatch) {
					t.Fatalf("want dynamic count mismatch, got %v", err)
				}
			})
		}
	}
}

func TestRound6StrictDeferredCountIdentity(t *testing.T) {
	for _, entry := range []string{"Complete", "DeferredSpec", "DeferredPatch"} {
		t.Run(entry, func(t *testing.T) {
			req := &pb.Account{Members: []*pb.Owner{{Note: "same"}, {Note: "extra"}}}
			stored := &pb.Account{Members: []*pb.Owner{{Note: "same"}}}

			_, err := round2Entry(t, entry, req, stored, maskOf([]string{"members.*.note"}), update.WithImmutableFields("members"), update.WithStrict(true))
			if !errors.Is(err, update.ErrElementCountMismatch) || errors.Is(err, update.ErrProtectedField) || errors.Is(err, update.ErrBehaviorViolation) {
				t.Fatalf("count mismatch must retain its identity without a protected-field violation: %v", err)
			}
		})
	}
}

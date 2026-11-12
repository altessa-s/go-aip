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
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	maskpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func TestCompleteStoredRequiredElements(t *testing.T) {
	req := &pb.Account{}
	stored := &pb.Account{Members: []*pb.Owner{{Email: "e"}}}
	before := proto.Clone(stored)
	raw := maskOf([]string{"members.*.email"})

	pending, err := update.Prepare(raw, req)
	if err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, &pb.Account{}, req)

	mask, err := pending.Complete(stored)

	be, ok := errors.AsType[*update.BehaviorError](err)
	if !ok || len(be.Violations) != 1 || be.Violations[0].Path != "members.0.email" || be.Violations[0].Behavior != annotations.FieldBehavior_REQUIRED {
		t.Fatalf("want REQUIRED members.0.email, got mask=%v err=%v", mask, err)
	}

	if len(mask) != 0 {
		t.Fatalf("failed completion returned writable mask %v", mask)
	}

	requireProtoEqual(t, &pb.Account{}, req)
	requireProtoEqual(t, before, stored)
	checkDeferredEquivalence(t, raw, &pb.Account{}, stored)
}

func TestPrepareDefersTemporaryPinning(t *testing.T) {
	for _, key := range []string{"c", "*"} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("key=%s/nested=%t", key, nested), func(t *testing.T) {
				account := &pb.Account{Delegates: map[string]*pb.Owner{
					"k": {Email: "e", Org: "same"}, "a": {Email: "e", Note: "n"}, "b": {Email: "e", Note: "n"}, key: {Email: "e", Note: "n"},
				}}
				req := account
				paths := []string{"delegates.*.note", "delegates.k"}

				if nested {
					req = &pb.Account{Parent: account}
					paths = []string{"parent.delegates.*.note", "parent.delegates.k"}
				}

				stored := proto.CloneOf(req)
				original := proto.CloneOf(req)
				opts := []update.Option{update.WithMaxPaths(2)}
				direct := proto.CloneOf(req)

				want, err := update.ApplyMask(maskOf(paths), direct, append(slices.Clone(opts), update.WithCurrent(stored))...)
				if err != nil {
					t.Fatal(err)
				}

				if !slices.Equal(want.ToPaths(), paths) {
					t.Fatalf("direct mask=%v want=%v", want.ToPaths(), paths)
				}

				pending, err := update.Prepare(maskOf(paths), req, opts...)
				if err != nil {
					t.Fatal(err)
				}

				filtered := proto.CloneOf(original)

				selected := filtered
				if nested {
					selected = filtered.Parent
				}

				for k, owner := range selected.Delegates {
					if k != "k" {
						owner.Email = "" // only note is selected on wildcard elements
					}
				}

				requireProtoEqual(t, filtered, req)

				mask, err := pending.Complete(stored)
				if err != nil {
					t.Fatal(err)
				}

				if !slices.Equal(mask.ToPaths(), paths) {
					t.Fatalf("final mask=%v want=%v", mask.ToPaths(), paths)
				}

				merged := proto.Clone(stored)
				if err := update.MergePaths(merged, req, mask.ToPaths(), opts...); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, original, merged)
			})
		}
	}
}

func TestProtectedDescendantIgnoresParentPresence(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "owner" || fd.Name() == "members" || fd.Name() == "delegates" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, tc := range []struct {
			path   string
			stored *pb.Account
		}{
			{"owner.note", &pb.Account{Owner: &pb.Owner{Email: "e"}}},
			{"parent.owner.note", &pb.Account{Parent: &pb.Account{Owner: &pb.Owner{Email: "e"}}}},
			{"members.*.note", &pb.Account{Members: []*pb.Owner{{Email: "e"}}}},
			{"delegates.k.note", &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e"}}}},
		} {
			for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "Patch", "DeferredSpec", "DeferredPatch"} {
				t.Run(fmt.Sprintf("%s/%s/%s", b, tc.path, entry), func(t *testing.T) {
					req := &pb.Account{}

					mask, err := round2Entry(t, entry, req, tc.stored, maskOf([]string{tc.path}), update.WithResolver(resolver))
					if err != nil {
						t.Fatal(err)
					}

					if len(mask) != 0 {
						t.Fatalf("no-op mask=%v", mask.ToPaths())
					}

					requireProtoEqual(t, &pb.Account{}, req)

					merged := proto.Clone(tc.stored)
					if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, tc.stored, merged)
				})
			}
		}
	}
	// Exercise the policy declaration independently of the resolver.
	req := &pb.Account{}

	mask, err := update.ApplyMask(maskOf([]string{"owner.note"}), req, update.WithImmutableFields("owner"), update.WithCurrent(&pb.Account{Owner: &pb.Owner{Email: "e"}}))
	if err != nil || len(mask) != 0 {
		t.Fatalf("policy no-op: mask=%v err=%v", mask, err)
	}
}

func TestPrepareDefersNestedPinning(t *testing.T) {
	md := nestedMapDescriptor(t)
	req := dynamicpb.NewMessage(md)

	text := `inners { m { key: "k" value { a: "same" } } m { key: "j" value { b: "new" } } }
 inners { m { key: "k" value { a: "same" } } m { key: "z" value { b: "new" } } }`
	if err := prototext.Unmarshal([]byte(text), req); err != nil {
		t.Fatal(err)
	}

	stored := proto.Clone(req)
	paths := []string{"inners.*.m.*.b", "inners.*.m.k"}

	pending, err := update.Prepare(maskOf(paths), req, update.WithMaxPaths(2))
	if err != nil {
		t.Fatal(err)
	}

	if len(pending.Mask()) != 0 {
		t.Fatalf("interim mask must defer wildcard and excluded key together: %v", pending.Mask())
	}

	requireProtoEqual(t, stored, req)

	mask, err := pending.Complete(stored)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(mask.ToPaths(), paths) {
		t.Fatalf("mask=%v want=%v", mask.ToPaths(), paths)
	}

	merged := proto.Clone(stored)
	if err := update.MergePaths(merged, req, mask.ToPaths(), update.WithMaxPaths(2)); err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, stored, merged)
	checkDeferredEquivalence(t, maskOf(paths), proto.Clone(stored), stored, update.WithMaxPaths(2))
}

func TestCompletionIndependenceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		paths        []string
		req          *pb.Account
		opts         []update.Option
		needsCurrent bool
	}{
		{name: "empty", paths: []string{}, req: &pb.Account{Region: "unselected"}},
		{name: "scalar", paths: []string{"display_name"}, req: &pb.Account{DisplayName: "new", Region: "unselected"}},
		{name: "ignored", paths: []string{"owner.create_time"}, req: &pb.Account{}, opts: []update.Option{update.WithImmutableFields("owner")}},
		{name: "message leaf", paths: []string{"owner.note"}, req: &pb.Account{}},
		{name: "absent list", paths: []string{"members.*.email"}, req: &pb.Account{}, needsCurrent: true},
		{name: "covering", paths: []string{"settings"}, req: &pb.Account{}, needsCurrent: true},
		{name: "protected clear", paths: []string{"region"}, req: &pb.Account{}, needsCurrent: true},
		{name: "unnamed identifier", paths: []string{"display_name"}, req: &pb.Account{Name: "n", DisplayName: "new"}, needsCurrent: true},
		{name: "retained routing message", paths: []string{"display_name"}, req: &pb.Account{DisplayName: "new", Owner: &pb.Owner{Org: "request"}},
			opts: []update.Option{update.WithIdentityFields("owner")}, needsCurrent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := proto.Clone(tc.req)

			pending, err := update.Prepare(maskOf(tc.paths), tc.req, tc.opts...)
			if err != nil {
				t.Fatal(err)
			}

			_, err = pending.Complete(nil)
			if tc.needsCurrent {
				if !errors.Is(err, update.ErrMessageMismatch) {
					t.Fatalf("stored state required, got %v", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}
			// A completely unrelated stored type is never inspected on this path.
			mask, err := pending.Complete(&pb.Owner{})
			if err != nil || !slices.Equal(mask.ToPaths(), pending.Mask().ToPaths()) {
				t.Fatalf("independent mask=%v err=%v", mask, err)
			}

			checkDeferredEquivalence(t, maskOf(tc.paths), before, &pb.Account{}, tc.opts...)
		})
	}
}

func TestRetainedRoutingMessageCompletion(t *testing.T) {
	req := &pb.Account{DisplayName: "new", Owner: &pb.Owner{Email: "e", Org: "request", CreateTime: "client"}}
	stored := &pb.Account{DisplayName: "old", Owner: &pb.Owner{Email: "e", Org: "stored", CreateTime: "server"}}

	pending, err := update.Prepare(maskOf([]string{"display_name"}), req, update.WithIdentityFields("owner"))
	if err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, &pb.Account{DisplayName: "new", Owner: &pb.Owner{Email: "e", Org: "request"}}, req)

	mask, err := pending.Complete(stored)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(mask.ToPaths(), []string{"display_name"}) {
		t.Fatal(mask)
	}

	requireProtoEqual(t, &pb.Account{DisplayName: "new", Owner: &pb.Owner{Email: "e", Org: "stored"}}, req)

	merged := proto.CloneOf(stored)
	if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, &pb.Account{DisplayName: "new", Owner: &pb.Owner{Email: "e", Org: "stored", CreateTime: "server"}}, merged)
}

func TestProtectedSelectedMutation(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "owner" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, tc := range []struct {
			path        string
			req, stored *pb.Account
			changed     bool
		}{
			{"owner.note", &pb.Account{}, &pb.Account{Owner: &pb.Owner{}}, false},
			// Traversal materializes owner, changing the protected field's presence.
			{"owner.note", &pb.Account{Owner: &pb.Owner{Email: "unselected"}}, &pb.Account{}, true},
			{"owner.note", &pb.Account{}, &pb.Account{Owner: &pb.Owner{Note: "selected"}}, true},
			{"owner", &pb.Account{}, &pb.Account{Owner: &pb.Owner{}}, true},
		} {
			for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "DeferredSpec", "Patch", "DeferredPatch"} {
				t.Run(fmt.Sprintf("%s/%s/%s/%v", b, tc.path, entry, tc.stored), func(t *testing.T) {
					req := proto.CloneOf(tc.req)

					mask, err := round2Entry(t, entry, req, tc.stored, maskOf([]string{tc.path}), update.WithResolver(resolver))
					if tc.changed {
						if err == nil {
							t.Fatal("selected change accepted")
						}

						switch entry {
						case "Spec", "DeferredSpec", "Patch", "DeferredPatch":
							if !errors.Is(err, update.ErrProtectedField) {
								t.Fatalf("want protected field refusal, got %v", err)
							}
						default:
							violations := []update.FieldViolation{}
							if be, ok := errors.AsType[*update.BehaviorError](err); ok {
								violations = be.Violations
							}

							if se, ok := errors.AsType[*update.StrictError](err); ok {
								violations = se.Violations
							}

							if len(violations) != 1 || violations[0].Path != "owner" || violations[0].Behavior != b {
								t.Fatalf("want %s owner, got %v", b, err)
							}
						}

						want := proto.CloneOf(tc.req)
						if (entry == "DeferredSpec" || entry == "DeferredPatch") &&
							b == annotations.FieldBehavior_IMMUTABLE && tc.path == "owner.note" && want.Owner != nil {
							want.Owner.Email = "" // unselected input is absent at the boundary
						}

						requireProtoEqual(t, want, req)

						return
					}

					if err != nil || len(mask) != 0 {
						t.Fatalf("no-op: mask=%v err=%v", mask, err)
					}

					requireProtoEqual(t, tc.req, req)
				})
			}
		}

		for _, stateful := range []bool{false, true} {
			for _, entry := range []string{"Spec", "Patch"} {
				t.Run(fmt.Sprintf("strip/%s/stateful=%t/%s", b, stateful, entry), func(t *testing.T) {
					req, stored := &pb.Account{}, &pb.Account{Owner: &pb.Owner{Email: "e"}}

					opts := []fieldbehavior.Option{fieldbehavior.WithResolver(resolver)}
					if stateful {
						opts = append(opts, fieldbehavior.WithCurrent(stored))
					}

					if err := fieldbehavior.StripUpdate(req, opts...); err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, &pb.Account{}, req)

					mask, err := round2Entry(t, entry, req, stored, maskOf([]string{"owner.note"}), update.WithResolver(resolver))
					if err != nil || len(mask) != 0 {
						t.Fatalf("stripped no-op: mask=%v err=%v", mask, err)
					}

					requireProtoEqual(t, &pb.Account{}, req)
				})
			}
		}
	}
}

func TestProtectedDynamicSelection(t *testing.T) {
	stored := &maskpb.UpdateRequest{ExtraData: &structpb.Struct{Fields: map[string]*structpb.Value{"unselected": structpb.NewStringValue("keep")}}}

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "extra_data" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, path := range []string{"extra_data.missing", "extra_data.missing.child"} {
			req := &maskpb.UpdateRequest{}

			mask, err := update.ApplyMask(maskOf([]string{path}), req, update.WithResolver(resolver), update.WithCurrent(stored))
			if err != nil || len(mask) != 0 {
				t.Fatalf("%s %s no-op: mask=%v err=%v", b, path, mask, err)
			}

			requireProtoEqual(t, &maskpb.UpdateRequest{}, req)

			merged := proto.Clone(stored)
			if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
				t.Fatal(err)
			}

			requireProtoEqual(t, stored, merged)
			checkDeferredEquivalence(t, maskOf([]string{path}), req, stored, update.WithResolver(resolver))
		}
	}
}

func TestEarlyRequiredPrecedesDeferredLimits(t *testing.T) {
	req := &pb.Resource{}
	raw := maskOf([]string{"*"})
	pending, err := update.Prepare(raw, req, update.WithMaxPaths(1))

	be, ok := errors.AsType[*update.BehaviorError](err)
	if pending != nil || !ok || len(be.Violations) != 1 || be.Violations[0].Path != "name" || be.Violations[0].Behavior != annotations.FieldBehavior_REQUIRED {
		t.Fatalf("want early REQUIRED name, got plan=%v err=%v", pending, err)
	}

	requireProtoEqual(t, &pb.Resource{}, req)

	mask, err := update.ApplyMask(raw, req, update.WithMaxPaths(1), update.WithCurrent(&pb.Resource{}))
	if mask != nil || !errors.Is(err, fieldmask.ErrMaxPathsExceeded) {
		t.Fatalf("want final expansion limit, got mask=%v err=%v", mask, err)
	}

	requireProtoEqual(t, &pb.Resource{}, req)
}

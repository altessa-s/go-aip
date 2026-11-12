// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// round2Entry checks the public validation paths. Patch also checks its actual
// returned resource; the others merge only their returned effective paths.
func round2Entry(t *testing.T, entry string, req, stored proto.Message, raw *fieldmaskpb.FieldMask, opts ...update.Option) (fieldmask.FieldMask, error) {
	t.Helper()

	current := append(slices.Clone(opts), update.WithCurrent(stored))

	mask := fieldmask.FromPaths(raw.GetPaths()...)
	if raw == nil {
		mask = nil
	}

	switch entry {
	case "ApplyMask":
		return update.ApplyMask(raw, req, current...)
	case "ApplyMaskStrict":
		return update.ApplyMask(raw, req, append(current, update.WithStrict(true))...)
	case "Apply":
		return mask, update.Apply(mask, req, current...)
	case "ValidateStrict":
		return update.ValidateStrict(mask, req, current...)
	case "Complete":
		pending, err := update.Prepare(raw, req, opts...)
		if err != nil {
			return nil, err
		}

		return pending.Complete(stored)
	}

	spec, err := update.NewSpec(req, opts...)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	if entry == "DeferredSpec" || entry == "DeferredPatch" {
		pending, err := update.Prepare(raw, req, opts...)
		if err != nil {
			return nil, err
		}

		ctx, raw = update.ContextWithPlan(ctx, pending), pending.Mask().ToProtoFieldMask()
	}

	if entry == "Spec" || entry == "DeferredSpec" {
		plan, err := spec.Plan(req, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
		return fieldmask.FromPaths(plan.Paths...), err
	}

	mask = fieldmask.FieldMask{}
	patch := update.Patch[proto.Message, proto.Message, *int64]{
		Field: "resource", Spec: spec,
		Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
		Read:         func(context.Context) (proto.Message, error) { return proto.Clone(stored), nil },
		ToProto:      func(m proto.Message) proto.Message { return m },
		Save: func(_ context.Context, m proto.Message, plan update.Plan, _ *int64) (proto.Message, error) {
			mask = fieldmask.FromPaths(plan.Paths...)
			return m, nil
		},
	}

	got, err := patch.Run(ctx, req, "", raw)
	if err == nil {
		requireProtoEqual(t, stored, got)
	}

	return mask, err
}

func TestIgnoredProtectedSelection(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "owner" || fd.Name() == "members" || fd.Name() == "delegates" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, tc := range []struct {
			path string
			full *pb.Account
		}{
			{"owner", &pb.Account{Owner: &pb.Owner{Email: "e", Note: "n", CreateTime: "server"}}},
			{"parent.owner", &pb.Account{Parent: &pb.Account{Owner: &pb.Owner{Email: "e", Note: "n", CreateTime: "server"}}}},
			{"members.*", &pb.Account{Members: []*pb.Owner{{Email: "e", Note: "n", CreateTime: "server"}}}},
			{"delegates.k", &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e", Note: "n", CreateTime: "server"}}}},
		} {
			for _, reverse := range []bool{false, true} {
				for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "Patch", "DeferredSpec", "DeferredPatch"} {
					t.Run(fmt.Sprintf("%s/%s/reverse=%t/%s", b, tc.path, reverse, entry), func(t *testing.T) {
						req, stored := &pb.Account{}, proto.Clone(tc.full).(*pb.Account)
						if reverse {
							req, stored = stored, req
						}

						before := proto.Clone(req)

						mask, err := round2Entry(t, entry, req, stored, maskOf([]string{tc.path + ".create_time"}), update.WithResolver(resolver))
						if err != nil {
							t.Fatal(err)
						}

						if len(mask) != 0 {
							t.Fatalf("ignored selection returned mask %v", mask.ToPaths())
						}

						wantInput := proto.Clone(before).(*pb.Account)

						if reverse && (entry != "ValidateStrict" && entry != "Spec" && entry != "Patch") {
							switch tc.path {
							case "owner":
								wantInput.Owner.CreateTime = ""
							case "parent.owner":
								wantInput.Parent.Owner.CreateTime = ""
							case "members.*":
								wantInput.Members[0].CreateTime = ""
							case "delegates.k":
								wantInput.Delegates["k"].CreateTime = ""
							}
						}

						requireProtoEqual(t, wantInput, req)

						if entry != "Patch" {
							merged := proto.Clone(stored)
							if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
								t.Fatal(err)
							}

							requireProtoEqual(t, stored, merged)
						}
					})
				}
			}
		}
	}
}

func TestProtectedSelectionStillChecksClear(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "owner" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, paths := range [][]string{{"owner"}, {"owner.email"}, {"owner.email", "owner.create_time"}} {
			for _, deferred := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%v/deferred=%t", b, paths, deferred), func(t *testing.T) {
					req := &pb.Account{}
					stored := &pb.Account{Owner: &pb.Owner{Email: "e", CreateTime: "server"}}

					entry := "ApplyMask"
					if deferred {
						entry = "Complete"
					}

					_, err := round2Entry(t, entry, req, stored, maskOf(paths), update.WithResolver(resolver))

					be, ok := errors.AsType[*update.BehaviorError](err)
					if !ok || len(be.Violations) != 1 || be.Violations[0].Path != "owner" || be.Violations[0].Behavior != b {
						t.Fatalf("want one %s violation at owner, got %v", b, err)
					}

					requireProtoEqual(t, &pb.Account{}, req)
				})
			}
		}
	}
}

func TestPrepareIgnoredPolicySelection(t *testing.T) {
	for name, policy := range map[string]update.Option{
		"immutable": update.WithImmutableFields("owner"),
		"identity":  update.WithIdentityFields("owner"),
	} {
		for _, present := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/present=%t", name, present), func(t *testing.T) {
				req, want := &pb.Account{}, &pb.Account{}
				if present {
					req.Owner = &pb.Owner{Email: "e", Org: "org", CreateTime: "client"}
					want.Owner = &pb.Owner{Email: "e", Org: "org"}
				}

				pending, err := update.Prepare(maskOf([]string{"owner.create_time"}), req, policy)
				if err != nil {
					t.Fatal(err)
				}

				if len(pending.Mask()) != 0 || len(pending.Operations()) != 0 {
					t.Fatalf("ignored selection retained work: mask=%v operations=%v", pending.Mask(), pending.Operations())
				}

				requireProtoEqual(t, want, req)

				mask, err := pending.Complete(nil) // no comparison should read stored state
				if err != nil || len(mask) != 0 {
					t.Fatalf("ignored completion: %v, %v", mask, err)
				}

				requireProtoEqual(t, want, req)
			})
		}
	}
}

func TestStatelessProtectedOutputOnly(t *testing.T) {
	file := protodesc.ToFileDescriptorProto(protectedOutputOnlyDescriptor(t).ParentFile())
	file.MessageType[0].Field = append(file.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{
		Name: new("title"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	})

	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	md := fd.Messages().ByName("Root")
	parse := func(stamp, title string) proto.Message {
		m := dynamicpb.NewMessage(md)

		text := fmt.Sprintf(`title: %q settings { config { x: "x" server: %q } configs { x: "y" server: %q }
			named { key: "k" value { x: "z" server: %q } } note: "new" }`, title, stamp, stamp, stamp)
		if err := prototext.Unmarshal([]byte(text), m); err != nil {
			t.Fatal(err)
		}

		return m
	}

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			got := fieldbehavior.AnnotationResolver{}.Resolve(fd)
			if slices.Contains(got, annotations.FieldBehavior_IMMUTABLE) {
				return []annotations.FieldBehavior{b}
			}

			return got
		})
		opts := []update.Option{atomicNames("protectedoo.Settings"), update.WithResolver(resolver)}

		for _, sibling := range []bool{false, true} {
			for _, entry := range []string{"ApplyMask", "Apply", "Prepare"} {
				t.Run(fmt.Sprintf("%s/sibling=%t/%s", b, sibling, entry), func(t *testing.T) {
					req, stored := parse("client", "new"), parse("server", "old")
					paths := []string{"settings"}
					wantPaths := []string{}
					wantTitle := "old"

					if sibling {
						paths = append(paths, "title")
						wantPaths, wantTitle = []string{"title"}, "new"
					}

					var pending *update.DeferredPlan

					var mask fieldmask.FieldMask

					var err error

					switch entry {
					case "ApplyMask":
						mask, err = update.ApplyMask(maskOf(paths), req, opts...)
					case "Apply":
						mask = fieldmask.FromPaths(paths...)
						err = update.Apply(mask, req, opts...)
					case "Prepare":
						pending, err = update.Prepare(maskOf(paths), req, opts...)
						if err == nil {
							mask = pending.Mask()
						}
					}

					if err != nil {
						t.Fatal(err)
					}

					if !slices.Equal(mask.ToPaths(), wantPaths) {
						t.Fatalf("mask = %v, want %v", mask.ToPaths(), wantPaths)
					}

					requireProtoEqual(t, parse("", "new"), req)

					if pending != nil {
						mask, err = pending.Complete(stored)
						if err != nil {
							t.Fatal(err)
						}

						if !slices.Equal(mask.ToPaths(), paths) {
							t.Fatalf("completed mask = %v, want %v", mask.ToPaths(), paths)
						}

						merged := proto.Clone(stored)
						if err := update.MergePaths(merged, req, mask.ToPaths(), opts...); err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, parse("server", wantTitle), merged)
					}
				})
			}
		}
	}
}

func TestDeferredAbsentExpansionLimits(t *testing.T) {
	resolver := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.FullName() == "fieldbehaviortest.v1.Profile.id" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		}

		return fieldbehavior.AnnotationResolver{}.Resolve(fd)
	}))

	for _, tc := range []struct {
		name  string
		limit update.Option
		cause error
	}{
		{"depth", update.WithMaxPathDepth(1), fieldmask.ErrMaxPathDepthExceeded},
		{"count", update.WithMaxPaths(5), fieldmask.ErrMaxPathsExceeded},
		{"length", update.WithMaxPathLength(11), fieldmask.ErrMaxPathLengthExceeded},
	} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stored-profile=%t", tc.name, populated), func(t *testing.T) {
				req, stored := &pb.Resource{Name: "n"}, &pb.Resource{Name: "n"}
				if populated {
					stored.Profile = &pb.Profile{DisplayName: "old"}
				}

				pending, err := update.Prepare(maskOf([]string{"*"}), req, tc.limit, resolver)
				if err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, &pb.Resource{Name: "n"}, req)
				// Only known expansion paths count before completion.
				if !slices.Equal(pending.Mask().ToPaths(), []string{"aliases", "description", "labels", "name", "password"}) {
					t.Fatalf("initial mask: %v", pending.Mask().ToPaths())
				}

				mask, err := pending.Complete(stored)

				_, directErr := update.ApplyMask(maskOf([]string{"*"}), &pb.Resource{Name: "n"}, tc.limit, resolver, update.WithCurrent(stored))
				if populated {
					// Stored presence makes the deferred branch real and enforces its limits.
					if !errors.Is(err, tc.cause) || !errors.Is(directErr, tc.cause) {
						t.Fatalf("want final limit refusal %v: complete=%v direct=%v", tc.cause, err, directErr)
					}

					return
				}

				if err != nil || directErr != nil {
					t.Fatalf("complete=%v direct=%v", err, directErr)
				}

				if !slices.Equal(mask.ToPaths(), []string{"aliases", "description", "labels", "name", "password"}) {
					t.Fatalf("final mask: %v", mask.ToPaths())
				}

				merged := proto.Clone(stored)
				if err := update.MergePaths(merged, req, mask.ToPaths(), tc.limit); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, &pb.Resource{Name: "n"}, merged)
			})
		}
	}
}

func TestStatefulStripRetainedProtectedMessage(t *testing.T) {
	for _, outer := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, inner := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
			for _, known := range []bool{false, true} {
				for _, entry := range []string{"Spec", "Patch"} {
					t.Run(fmt.Sprintf("%s/%s/current=%t/%s", outer, inner, known, entry), func(t *testing.T) {
						resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
							switch fd.Name() {
							case "parent":
								return []annotations.FieldBehavior{outer}
							case "owner":
								return []annotations.FieldBehavior{inner}
							}

							return fieldbehavior.AnnotationResolver{}.Resolve(fd)
						})
						stored := &pb.Account{Owner: &pb.Owner{Email: "e", Org: "org", CreateTime: "server"},
							Parent: &pb.Account{Owner: &pb.Owner{Email: "nested", Org: "org", CreateTime: "server"}}}
						req := proto.Clone(stored).(*pb.Account)

						opts := []fieldbehavior.Option{fieldbehavior.WithResolver(resolver)}
						if known {
							opts = append(opts, fieldbehavior.WithCurrent(stored))
						}

						if err := fieldbehavior.StripUpdate(req, opts...); err != nil {
							t.Fatal(err)
						}

						wantInput := proto.Clone(stored).(*pb.Account)

						wantInput.Owner.CreateTime, wantInput.Parent.Owner.CreateTime = "", ""
						requireProtoEqual(t, wantInput, req)

						mask, err := round2Entry(t, entry, req, stored, nil, update.WithResolver(resolver))
						if err != nil {
							t.Fatal(err)
						}

						if len(mask) != 0 {
							t.Fatalf("mask = %v, want no writes", mask.ToPaths())
						}

						merged := proto.Clone(stored)
						if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, stored, merged)
					})
				}
			}
		}
	}
}

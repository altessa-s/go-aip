// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
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

func TestDeferredRecursiveReplacement(t *testing.T) {
	for _, paths := range [][]string{{"settings"}, {"settings.defaults"}} {
		t.Run(paths[0], func(t *testing.T) {
			stored := &pb.Account{Settings: &pb.Settings{Theme: "old", UpdatedBy: "server", Defaults: &pb.Settings{Theme: "old", UpdatedBy: "nested-server"}}}
			req := &pb.Account{Settings: &pb.Settings{Theme: "new"}}
			checkDeferredEquivalence(t, maskOf(paths), req, stored)

			pending, err := update.Prepare(maskOf(paths), req)
			if err != nil {
				t.Fatal(err)
			}

			store := &accountStore{cur: stored}

			got, err := store.patch(t).Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask())
			if err != nil {
				t.Fatal(err)
			}

			want := proto.Clone(stored).(*pb.Account)

			want.Settings.Defaults.Theme = ""
			if paths[0] == "settings" {
				want.Settings.Theme = "new"
			}

			requireProtoEqual(t, want, got)
			requireProtoEqual(t, want, store.saved)
		})
	}
}

func TestDeferredAllowMissingOriginal(t *testing.T) {
	for _, identifier := range []bool{false, true} {
		for _, validateOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("identifier=%t/validateOnly=%t", identifier, validateOnly), func(t *testing.T) {
				req := storedAccount()
				if !identifier {
					req.Name = ""
				}

				original := proto.Clone(req).(*pb.Account)
				raw := maskOf([]string{"display_name"})

				pending, err := update.Prepare(raw, req)
				if err != nil {
					t.Fatal(err)
				}

				store, direct := &accountStore{}, &accountStore{}
				opts := []update.PatchOption{update.WithAllowMissing(true), update.WithValidateOnly(validateOnly)}

				want, err := direct.patch(t).Run(t.Context(), original, "", raw, opts...)
				if err != nil {
					t.Fatal(err)
				}

				got, err := store.patch(t).Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask(), opts...)
				if err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, want, got)

				if !got.Enabled || got.Key == nil || got.Owner.Email == "" || got.Owner.CreateTime != "" {
					t.Fatalf("lost create input or sanitization: %v", got)
				}

				if validateOnly {
					if store.created != nil || store.saved != nil {
						t.Fatal("validate_only wrote the resource")
					}
				} else {
					requireProtoEqual(t, want, store.created)
				}
			})
		}
	}
}

func TestDeferredSecretConsumption(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for _, identifier := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("optional=%t/identifier=%t/empty=%t", optional, identifier, empty), func(t *testing.T) {
					var (
						req, stored proto.Message
						policy      update.Option
						secret      func(proto.Message) *string
					)

					path, secretName := "description", "password"

					id := ""
					if identifier {
						id = "r"
					}

					if optional {
						req = &descriptorpb.FileDescriptorProto{Package: new(id), Syntax: new("proto3"), Name: new("new-secret")}
						stored = &descriptorpb.FileDescriptorProto{Package: new(id), Syntax: new("proto2")}
						path, secretName = "syntax", "name"
						secret = func(m proto.Message) *string { return m.(*descriptorpb.FileDescriptorProto).Name }
						policy = update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
							switch fd.Name() {
							case "name":
								return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
							case "package":
								if identifier {
									return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}
								}
							}

							return nil
						}))
					} else {
						req = &pb.Resource{Id: id, Description: "new", Password: "new-secret"}
						stored = &pb.Resource{Id: id, Description: "old"}
						secret = func(m proto.Message) *string { return &m.(*pb.Resource).Password }
						policy = update.WithResolver(fieldbehavior.AnnotationResolver{})
					}

					paths := []string{path}
					if empty {
						paths = []string{}
					}

					raw := maskOf(paths)
					checkDeferredEquivalence(t, raw, req, stored, policy)
					wantMerged := proto.Clone(stored)

					if !empty {
						fd := req.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(path))
						wantMerged.ProtoReflect().Set(fd, req.ProtoReflect().Get(fd))
					}

					pending, err := update.Prepare(raw, req, policy)
					if err != nil {
						t.Fatal(err)
					}

					spec, err := update.NewSpec(req, policy)
					if err != nil {
						t.Fatal(err)
					}

					saves := 0
					patch := update.Patch[proto.Message, proto.Message, *int64]{
						Field: "resource", Spec: spec,
						Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
						Read:         func(context.Context) (proto.Message, error) { return proto.Clone(stored), nil },
						ToProto:      func(m proto.Message) proto.Message { return m },
						Save: func(_ context.Context, merged proto.Message, plan update.Plan, _ *int64) (proto.Message, error) {
							saves++

							value := plan.Secret(secretName, secret(req))
							if value == nil || *value != "new-secret" {
								t.Fatalf("Save lost bound secret: %v", value)
							}

							requireProtoEqual(t, wantMerged, merged)

							return merged, nil
						},
					}

					out, err := patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask())
					if err != nil {
						t.Fatal(err)
					}

					if empty {
						if saves != 0 {
							t.Fatal("empty mask saved secrets")
						}

						requireProtoEqual(t, stored, out)
					} else if saves != 1 {
						t.Fatal("secret update was not saved")
					}
				})
			}
		}
	}
}

func TestAtomicCollectionProtectedMessage(t *testing.T) {
	file := protodesc.ToFileDescriptorProto(protectedOutputOnlyDescriptor(t).ParentFile())
	settings := file.MessageType[1]
	settings.Field[1].Options = nil
	settings.Field[1].TypeName = new(".protectedoo.Element")
	settings.Field[2].Options = nil
	settings.NestedType[0].Field[1].TypeName = new(".protectedoo.Element")
	locked := proto.Clone(settings.Field[0]).(*descriptorpb.FieldDescriptorProto)
	locked.Name, locked.JsonName = new("locked"), new("locked")
	file.MessageType = append(file.MessageType, &descriptorpb.DescriptorProto{Name: new("Element"), Field: []*descriptorpb.FieldDescriptorProto{locked}})

	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	md := fd.Messages().ByName("Root")

	for _, collection := range []string{`configs { locked { server: "saved" x: "a" } }`, `named { key: "k" value { locked { server: "saved" x: "a" } } }`} {
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deferred=%t", collection, deferred), func(t *testing.T) {
				parse := func(note string) *dynamicpb.Message {
					m := dynamicpb.NewMessage(md)
					if err := prototext.Unmarshal([]byte(`settings { `+collection+` note: "`+note+`" }`), m); err != nil {
						t.Fatal(err)
					}

					return m
				}
				stored, req := parse("old"), parse("new")
				want := proto.Clone(req)
				policy := atomicNames("protectedoo.Settings")
				raw := maskOf([]string{"settings"})
				checkDeferredEquivalence(t, raw, req, stored, policy)
				filtered, merged := proto.Clone(req), proto.Clone(stored)

				mask, err := update.ApplyMask(raw, filtered, policy, update.WithCurrent(stored))
				if err != nil {
					t.Fatal(err)
				}

				if err := update.MergePaths(merged, filtered, mask.ToPaths(), policy); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, want, merged)

				// StripUpdate sanitizes ignored descendants before a direct Plan
				// caller merges the validated replacement.
				stripped := proto.Clone(req)
				if err := fieldbehavior.StripUpdate(stripped); err != nil {
					t.Fatal(err)
				}

				directSpec, err := update.NewSpec(stripped, policy)
				if err != nil {
					t.Fatal(err)
				}

				directPlan, err := directSpec.Plan(stripped, raw, update.WithCurrent(stored))
				if err != nil {
					t.Fatal(err)
				}

				directMerged := proto.Clone(stored)
				if err := update.MergePaths(directMerged, stripped, directPlan.Paths, policy); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, want, directMerged)

				ctx := t.Context()

				if deferred {
					pending, err := update.Prepare(raw, req, policy)
					if err != nil {
						t.Fatal(err)
					}

					ctx, raw = update.ContextWithPlan(ctx, pending), pending.Mask().ToProtoFieldMask()
				}

				spec, err := update.NewSpec(req, policy)
				if err != nil {
					t.Fatal(err)
				}

				patch := update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64]{
					Field: "root", Spec: spec,
					Precondition: update.Int64Precondition[*dynamicpb.Message]{Version: func(*dynamicpb.Message) int64 { return 1 }},
					Read: func(context.Context) (*dynamicpb.Message, error) {
						return proto.Clone(stored).(*dynamicpb.Message), nil
					},
					ToProto: func(m *dynamicpb.Message) *dynamicpb.Message { return m },
					Save: func(_ context.Context, merged *dynamicpb.Message, _ update.Plan, _ *int64) (*dynamicpb.Message, error) {
						requireProtoEqual(t, want, merged)
						return merged, nil
					},
				}

				got, err := patch.Run(ctx, req, "", raw)
				if err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, want, got)
			})
		}
	}
}

func TestStripUpdateProtectedMessage(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, changed := range []bool{false, true} {
			for _, entry := range []string{"spec", "patch"} {
				t.Run(fmt.Sprintf("%s/changed=%t/%s", b, changed, entry), func(t *testing.T) {
					resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
						if fd.FullName() == "fieldbehaviortest.v1.Account.owner" {
							return []annotations.FieldBehavior{b}
						}

						return fieldbehavior.AnnotationResolver{}.Resolve(fd)
					})
					stored, req := storedAccount(), storedAccount()

					req.DisplayName = "new"
					if changed {
						req.Owner.Email = "changed@example.com"
					}

					raw := maskOf(nil)
					checkDeferredEquivalence(t, raw, req, stored, update.WithResolver(resolver))

					spec, err := update.NewSpec(req, update.WithResolver(resolver))
					if err != nil {
						t.Fatal(err)
					}

					_, originalErr := spec.Plan(req, nil, update.WithCurrent(stored))
					if err := fieldbehavior.StripUpdate(req, fieldbehavior.WithResolver(resolver)); err != nil {
						t.Fatal(err)
					}

					if req.Owner.CreateTime != "" {
						t.Fatal("StripUpdate retained OUTPUT_ONLY input")
					}

					var got proto.Message

					if entry == "spec" {
						plan, planErr := spec.Plan(req, nil, update.WithCurrent(stored))

						err = planErr
						if err == nil {
							got = proto.Clone(stored)
							err = update.MergePaths(got, req, plan.Paths, update.WithResolver(resolver))
						}
					} else {
						store := &accountStore{cur: stored}
						patch := store.patch(t)
						patch.Spec = spec
						got, err = patch.Run(t.Context(), req, "", nil)
					}

					if changed {
						if !errors.Is(err, update.ErrProtectedField) || !errors.Is(originalErr, update.ErrProtectedField) {
							t.Fatalf("changed protected input accepted: original=%v stripped=%v", originalErr, err)
						}
					} else {
						if originalErr != nil || err != nil {
							t.Fatalf("equal protected input rejected: original=%v stripped=%v", originalErr, err)
						}

						want := proto.Clone(stored).(*pb.Account)
						want.DisplayName = "new"
						requireProtoEqual(t, want, got)
					}
				})
			}
		}
	}
}

func TestDeferredNoComparisonSnapshot(t *testing.T) {
	req := &pb.Account{DisplayName: "new", Enabled: true}
	raw := maskOf([]string{"display_name"})

	pending, err := update.Prepare(raw, req)
	if err != nil {
		t.Fatal(err)
	}

	req.DisplayName = "later mutation"
	raw.Paths[0] = "enabled"

	mask, err := pending.Complete(nil)
	if err != nil {
		t.Fatal(err)
	}

	merged := &pb.Account{DisplayName: "old", Enabled: true}
	if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
		t.Fatal(err)
	}

	requireProtoEqual(t, &pb.Account{DisplayName: "new", Enabled: true}, merged)
}

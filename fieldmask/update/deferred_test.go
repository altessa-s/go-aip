// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestProtectedUpdateEntryPoints checks both protected behaviors through every
// mask mode and entry point, including full collection replacements. The request
// and stored messages deliberately differ in their mutable values.
func TestProtectedUpdateEntryPoints(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		field  string
		paths  []string
		change func(*pb.Account, string)
	}{
		{"omitted", "region", nil, func(a *pb.Account, v string) { a.Region = v }},
		{"all", "region", []string{"*"}, func(a *pb.Account, v string) { a.Region = v }},
		{"explicit", "region", []string{"region", "display_name"}, func(a *pb.Account, v string) { a.Region = v }},
		{"nested", "owner.org", []string{"owner.org", "owner.note"}, func(a *pb.Account, v string) { a.Owner.Org = v }},
		{"covering", "owner.org", []string{"owner"}, func(a *pb.Account, v string) { a.Owner.Org = v }},
		{"covered", "owner.org", []string{"owner", "owner.org"}, func(a *pb.Account, v string) { a.Owner.Org = v }},
		{"list", "members.0.org", []string{"members"}, func(a *pb.Account, v string) { a.Members[0].Org = v }},
		{"list_element", "members.0.org", []string{"members.*"}, func(a *pb.Account, v string) { a.Members[0].Org = v }},
		{"list_leaf", "members.org", []string{"members.*.org", "members.*.note"}, func(a *pb.Account, v string) { a.Members[0].Org = v }},
		{"map", "delegates.k.org", []string{"delegates"}, func(a *pb.Account, v string) { a.Delegates["k"].Org = v }},
		{"map_element", "delegates.k.org", []string{"delegates.k"}, func(a *pb.Account, v string) { a.Delegates["k"].Org = v }},
		{"map_leaf", "delegates.org", []string{"delegates.k.org", "delegates.k.note"}, func(a *pb.Account, v string) { a.Delegates["k"].Org = v }},
	}

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" || fd.Name() == "region" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})
		policy := update.WithResolver(resolver)

		for _, tc := range cases {
			for _, known := range []bool{false, true} {
				for _, change := range []string{"same", "different", "clear"} {
					// An omitted mask cannot request a clear of an absent scalar.
					if tc.paths == nil && change == "clear" {
						continue
					}

					t.Run(fmt.Sprintf("%s/%s/current=%t/%s", b, tc.name, known, change), func(t *testing.T) {
						stored := storedAccount()
						req := storedAccount()
						req.DisplayName = "new"
						req.Owner.Note, req.Members[0].Note, req.Delegates["k"].Note = "new", "new", "new"

						if change != "same" {
							value := "changed"
							if change == "clear" {
								value = ""
							}

							tc.change(req, value)
						}

						opts := []update.Option{policy}
						if known {
							opts = append(opts, update.WithCurrent(stored))
						}

						if known {
							checkDeferredEquivalence(t, maskOf(tc.paths), req, stored, policy)
						}

						for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Plan", "Patch"} {
							if entry == "Patch" && !known {
								continue
							}

							t.Run(entry, func(t *testing.T) {
								msg := proto.Clone(req).(*pb.Account)

								mask, err := protectedEntry(t, entry, msg, stored, tc.paths, opts, policy)
								if known && change != "same" {
									if err == nil {
										t.Fatal("changed protected value was accepted")
									}

									field := tc.field
									if tc.name == "list_leaf" || tc.name == "map_leaf" {
										field = "org"
									}

									if !strings.Contains(err.Error(), field) {
										t.Fatalf("error does not name %s: %v", field, err)
									}

									switch entry {
									case "Plan", "Patch":
										if !errors.Is(err, update.ErrProtectedField) {
											t.Fatalf("wrong protected-field error identity: %v", err)
										}
									case "ApplyMaskStrict", "ValidateStrict":
										if !errors.Is(err, update.ErrInvalidUpdateMask) || !errors.Is(err, update.ErrBehaviorViolation) {
											t.Fatalf("wrong strict error identities: %v", err)
										}
									default:
										if _, ok := errors.AsType[*update.BehaviorError](err); !ok {
											t.Fatalf("wrong behavior error type: %v", err)
										}
									}

									if !proto.Equal(req, msg) {
										t.Fatal("failed update changed the request")
									}

									return
								}

								if err != nil {
									t.Fatal(err)
								}

								if !known && (msg.Region != req.Region || msg.GetOwner().GetOrg() != req.GetOwner().GetOrg() ||
									msg.GetMembers()[0].GetOrg() != req.GetMembers()[0].GetOrg() || msg.GetDelegates()["k"].GetOrg() != req.GetDelegates()["k"].GetOrg()) {
									t.Fatal("unverified protected input was lost")
								}

								merged := proto.Clone(stored).(*pb.Account)
								if err := update.Merge(mask, merged, msg, policy); err != nil {
									t.Fatal(err)
								}

								if merged.Region != stored.Region || merged.Owner.Org != stored.Owner.Org ||
									merged.Members[0].Org != stored.Members[0].Org || merged.Delegates["k"].Org != stored.Delegates["k"].Org {
									t.Fatal("effective mask overwrote a protected value")
								}
							})
						}
					})
				}
			}
		}
	}
}

func protectedEntry(t *testing.T, entry string, req, stored *pb.Account, paths []string, opts []update.Option, policy update.Option) (fieldmask.FieldMask, error) {
	t.Helper()

	mask := fieldmask.FromPaths(paths...)
	if paths == nil {
		mask = nil
	}

	switch entry {
	case "ApplyMask":
		return update.ApplyMask(maskOf(paths), req, opts...)
	case "ApplyMaskStrict":
		return update.ApplyMask(maskOf(paths), req, append(opts, update.WithStrict(true))...)
	case "Apply":
		if mask == nil {
			mask = update.FromSetFields(req, policy) // Apply(nil) cannot write back its synthesized mask
		}

		return mask, update.Apply(mask, req, opts...)
	case "ValidateStrict":
		return update.ValidateStrict(mask, req, opts...)
	}

	spec, err := update.NewSpec(req, policy)
	if err != nil {
		t.Fatal(err)
	}

	if entry == "Patch" {
		store := &accountStore{cur: proto.Clone(stored).(*pb.Account)}
		patch := store.patch(t)
		patch.Spec = spec

		out, err := patch.Run(t.Context(), req, "", maskOf(paths))
		if err != nil {
			return nil, err
		}

		if out.Region != stored.Region || out.Owner.Org != stored.Owner.Org ||
			out.Members[0].Org != stored.Members[0].Org || out.Delegates["k"].Org != stored.Delegates["k"].Org {
			t.Fatal("Patch overwrote a protected field")
		}
	}

	plan, err := spec.Plan(req, maskOf(paths), opts...)

	return fieldmask.FromPaths(plan.Paths...), err
}

func TestProtectedCollectionRemoval(t *testing.T) {
	t.Parallel()

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, path := range []string{"members", "members.*", "delegates", "delegates.*", "delegates.removed"} {
			for _, known := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/current=%t", b, path, known), func(t *testing.T) {
					stored := storedAccount()
					stored.Members = append(stored.Members, &pb.Owner{Email: "removed@example.com", Org: "removed"})
					stored.Delegates["removed"] = &pb.Owner{Email: "removed@example.com", Org: "removed"}
					req := storedAccount()
					before := proto.Clone(req)

					opts := []update.Option{update.WithResolver(resolver)}
					if known {
						opts = append(opts, update.WithCurrent(stored))
					}

					mask, err := update.ApplyMask(maskOf([]string{path}), req, opts...)

					if known {
						want := "delegates.removed.org"
						if strings.HasPrefix(path, "members") {
							want = "members.1.org"
						}

						if err == nil || !strings.Contains(err.Error(), want) || !proto.Equal(before, req) {
							t.Fatalf("removed protected value must fail atomically at %s: %v", want, err)
						}

						return
					}

					if err != nil || len(mask) != 0 {
						t.Fatalf("unverified replacement was not deferred: %v, %v", mask, err)
					}

					if req.Members[0].Org != stored.Members[0].Org || req.Delegates["k"].Org != stored.Delegates["k"].Org {
						t.Fatal("unverified request lost protected values")
					}
				})
			}
		}
	}
}

func TestImpliedProtectedOnlyInput(t *testing.T) {
	t.Parallel()

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" || fd.Name() == "region" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, nested := range []bool{false, true} {
			for _, known := range []bool{false, true} {
				for _, value := range []string{"old", "new"} {
					t.Run(fmt.Sprintf("%s/nested=%t/current=%t/%s", b, nested, known, value), func(t *testing.T) {
						req, stored := &pb.Account{Region: value}, &pb.Account{Region: "old"}
						if nested {
							req, stored = &pb.Account{Owner: &pb.Owner{Org: value}}, &pb.Account{Owner: &pb.Owner{Org: "old"}}
						}

						opts := []update.Option{update.WithResolver(resolver)}
						if known {
							opts = append(opts, update.WithCurrent(stored))
						}

						for _, entry := range []string{"ApplyMask", "Apply", "ValidateStrict", "Plan", "Patch"} {
							t.Run(entry, func(t *testing.T) {
								msg := proto.Clone(req)

								var err error

								switch entry {
								case "ApplyMask":
									_, err = update.ApplyMask(nil, msg, opts...)
								case "Apply":
									err = update.Apply(nil, msg, opts...)
								case "ValidateStrict":
									_, err = update.ValidateStrict(nil, msg, opts...)
								default:
									spec, specErr := update.NewSpec(req, update.WithResolver(resolver))
									if specErr != nil {
										t.Fatal(specErr)
									}

									if entry == "Patch" {
										if !known {
											return
										}

										store := &accountStore{cur: stored}
										patch := store.patch(t)
										patch.Spec = spec
										_, err = patch.Run(t.Context(), msg.(*pb.Account), "", nil)
									} else {
										_, err = spec.Plan(msg, nil, opts...)
									}
								}

								if known && value != "old" {
									if err == nil {
										t.Fatal("changed protected-only input was accepted")
									}
								} else if err != nil {
									t.Fatal(err)
								}

								if !known && !proto.Equal(req, msg) {
									t.Fatal("unverified protected-only input was discarded")
								}
							})
						}
					})
				}
			}
		}
	}
}

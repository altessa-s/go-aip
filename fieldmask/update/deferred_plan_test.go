// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// checkDeferredEquivalence runs the one-stage and two-stage calls from independent
// copies, compares diagnostics and request results (including refusals), and
// repeats completion. Conversion failures retain the pre-completion request.
func checkDeferredEquivalence(t *testing.T, raw *fieldmaskpb.FieldMask, request, stored proto.Message, opts ...update.Option) {
	t.Helper()

	one, two, before := proto.Clone(request), proto.Clone(request), proto.Clone(stored)

	want, wantErr := update.ApplyMask(raw, one, append(slices.Clone(opts), update.WithCurrent(stored))...)
	if wantErr == nil {
		update.AssertWritableSelection(t, raw, request, want, opts...)
	}

	pending, err := update.Prepare(raw, two, opts...)
	if err != nil {
		compareEarlyDeferredError(t, wantErr, err)

		if !proto.Equal(one, two) {
			t.Fatalf("preparation failure resource differs: one=%v two=%v", one, two)
		}

		return
	}

	initial := pending.Mask()
	update.AssertWritableSelection(t, raw, request, initial, opts...)

	for range 2 {
		beforeCompletion := proto.Clone(two)
		got, gotErr := pending.Complete(stored)
		compareDeferredError(t, wantErr, gotErr)

		expected := one
		if errors.Is(gotErr, update.ErrMessageMismatch) {
			expected = beforeCompletion
		}

		if !reflect.DeepEqual(want, got) || !proto.Equal(expected, two) {
			t.Fatalf("completion differs: masks %v / %v; resources %v / %v", want, got, one, two)
		}

		if wantErr == nil {
			update.AssertWritableSelection(t, raw, request, got, opts...)

			oneMerged, twoMerged := proto.Clone(stored), proto.Clone(stored)
			oneErr := update.MergePaths(oneMerged, one, want.ToPaths(), opts...)
			twoErr := update.MergePaths(twoMerged, two, got.ToPaths(), opts...)
			compareDeferredError(t, oneErr, twoErr)

			if !proto.Equal(oneMerged, twoMerged) {
				t.Fatalf("final merge differs: one=%v two=%v", oneMerged, twoMerged)
			}
		}

		if !proto.Equal(stored, before) || !reflect.DeepEqual(initial, pending.Mask()) {
			t.Fatal("completion mutated stored state or the initial effective mask")
		}
	}
}

func compareDeferredError(t *testing.T, want, got error) {
	t.Helper()

	if fmt.Sprint(want) != fmt.Sprint(got) || reflect.TypeOf(want) != reflect.TypeOf(got) {
		t.Fatalf("error differs: want %T %v; got %T %v", want, want, got, got)
	}

	for _, sentinel := range []error{update.ErrBehaviorViolation, update.ErrInvalidUpdateMask, update.ErrProtectedField,
		update.ErrMessageMismatch, fieldmask.ErrInvalidPath, fieldmask.ErrLimitExceeded} {
		if errors.Is(want, sentinel) != errors.Is(got, sentinel) {
			t.Fatalf("error identity differs for %v: %v / %v", sentinel, want, got)
		}
	}

	ws, wsok := errors.AsType[*update.StrictError](want)

	gs, gsok := errors.AsType[*update.StrictError](got)
	if wsok != gsok || (wsok && !reflect.DeepEqual(ws.Violations, gs.Violations)) {
		t.Fatalf("strict violations differ: %v / %v", ws, gs)
	}

	w, wok := errors.AsType[*update.BehaviorError](want)

	g, gok := errors.AsType[*update.BehaviorError](got)
	if wok != gok || (wok && !reflect.DeepEqual(w.Violations, g.Violations)) {
		t.Fatalf("violations differ: %v / %v", w, g)
	}
}

func TestDeferredPlanEquivalence(t *testing.T) {
	t.Parallel()

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" || fd.Name() == "region" {
				return []annotations.FieldBehavior{b}
			}

			// Isolate protected-field equivalence from the independent REQUIRED
			// refusal, which can happen before stored state is available.
			return slices.DeleteFunc(fieldbehavior.AnnotationResolver{}.Resolve(fd), func(v annotations.FieldBehavior) bool {
				return v == annotations.FieldBehavior_REQUIRED
			})
		})

		cases := []struct {
			name   string
			paths  []string
			atomic bool
		}{
			{"implied", nil, false}, {"empty", []string{}, false}, {"all", []string{"*"}, false},
			{"explicit", []string{"region", "displayName"}, false},
			{"nested", []string{"owner.org", "owner.note"}, false},
			{"covering", []string{"owner"}, false}, {"covered", []string{"owner", "owner.org"}, false},
			{"list", []string{"members"}, false}, {"list_elements", []string{"members.*"}, false},
			{"list_leaf", []string{"members.*.org", "members.*.note"}, false},
			{"map", []string{"delegates"}, false}, {"map_elements", []string{"delegates.*"}, false},
			{"map_key", []string{"delegates.k"}, false}, {"map_leaf", []string{"delegates.k.org", "delegates.k.note"}, false},
			{"atomic", []string{"owner"}, true}, {"atomic_list", []string{"members"}, true},
			{"atomic_map", []string{"delegates.k"}, true}, {"atomic_all", []string{"*"}, true},
			{"alias_cover", []string{"backupOwner", "backup_owner.org"}, false},
		}

		for _, tc := range cases {
			for _, state := range []string{"equal", "changed", "clear", "absent", "removed", "new"} {
				for _, strict := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/strict=%t", b, tc.name, state, strict), func(t *testing.T) {
						stored, req := storedAccount(), storedAccount()
						stored.BackupOwner, req.BackupOwner = proto.Clone(stored.Owner).(*pb.Owner), proto.Clone(req.Owner).(*pb.Owner)
						req.DisplayName = "updated"
						req.Owner.Note, req.BackupOwner.Note = "updated", "updated"
						req.Members[0].Note, req.Delegates["k"].Note = "updated", "updated"
						req.Owner.CreateTime = "client"

						switch state {
						case "changed", "clear":
							v := "changed"
							if state == "clear" {
								v = ""
							}

							req.Region, req.Owner.Org, req.BackupOwner.Org, req.Members[0].Org, req.Delegates["k"].Org = v, v, v, v, v
						case "absent":
							req.Region, req.Owner, req.BackupOwner, req.Members, req.Delegates = "", nil, nil, nil, nil
						case "removed":
							stored.Members = append(stored.Members, proto.Clone(stored.Members[0]).(*pb.Owner))
							stored.Delegates["removed"] = proto.Clone(stored.Owner).(*pb.Owner)
						case "new":
							stored.Region, stored.Owner, stored.BackupOwner, stored.Members, stored.Delegates = "", nil, nil, nil, nil
						}

						opts := []update.Option{update.WithResolver(resolver), update.WithStrict(strict)}
						if tc.atomic {
							opts = append(opts, atomicNames("fieldbehaviortest.v1.Owner"))
						}

						checkDeferredEquivalence(t, maskOf(tc.paths), req, stored, opts...)
					})
				}
			}
		}
	}
}

func TestDeferredPlanSnapshotAndContext(t *testing.T) {
	t.Parallel()

	req := storedAccount()
	req.Members[0].Note = "updated"
	original, stored := proto.Clone(req), storedAccount()
	raw := maskOf([]string{"members"})

	pending, err := update.Prepare(raw, req)
	if err != nil {
		t.Fatal(err)
	}

	ctx := update.ContextWithPlan(t.Context(), pending)
	if p, ok := update.PlanFromContext(ctx, req); !ok || p != pending {
		t.Fatal("bound resource did not recover its plan")
	}

	other := storedAccount()
	if _, ok := update.PlanFromContext(ctx, other); ok {
		t.Fatal("another resource picked up the plan")
	}

	second, err := update.Prepare(maskOf([]string{"region"}), other)
	if err != nil {
		t.Fatal(err)
	}

	ctx = update.ContextWithPlan(ctx, second)
	if p, ok := update.PlanFromContext(ctx, req); !ok || p != pending {
		t.Fatal("a stream message hid another message's plan")
	}

	raw.Paths[0] = "region"

	proto.Reset(req)

	ops := pending.Operations()
	if !slices.ContainsFunc(ops, func(op update.DeferredOperation) bool {
		return op.Path == "members" && op.Intent == update.DeferredReplace
	}) {
		t.Fatalf("replacement intent missing: %v", ops)
	}

	ops[0].Path = "corrupted"
	mask := pending.Mask()
	mask["corrupted"] = nil

	got, err := pending.Complete(stored)
	if err != nil || !slices.Equal(got.ToPaths(), []string{"members"}) || req.Members[0].Note != "updated" {
		t.Fatalf("snapshot was changed through request, raw mask or accessors: %v %v %v", got, req, err)
	}

	checkDeferredEquivalence(t, maskOf([]string{"members"}), original, stored)

	spec, err := update.NewSpec(&pb.Account{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := spec.Plan(other, nil, update.WithDeferredPlan(pending), update.WithCurrent(stored)); !errors.Is(err, update.ErrMessageMismatch) {
		t.Fatalf("explicit plan applied to another resource: %v", err)
	}
}

func TestDeferredPlanClearAndAllViolations(t *testing.T) {
	t.Parallel()

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "org" || fd.Name() == "note" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return fieldbehavior.AnnotationResolver{}.Resolve(fd)
	})
	stored := storedAccount()
	stored.Members[0].Note = "protected"
	stored.Members = append(stored.Members, &pb.Owner{Org: "removed", Note: "removed"})
	req := storedAccount()
	req.Region = ""
	req.Members[0].Org, req.Members[0].Note = "changed", "changed"

	pending, err := update.Prepare(maskOf([]string{"region", "members"}), req, update.WithResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(pending.Operations(), func(op update.DeferredOperation) bool {
		return op.Path == "region" && op.Intent == update.DeferredClear
	}) {
		t.Fatalf("explicit clear lost: %v", pending.Operations())
	}

	_, err = pending.Complete(stored)

	paths := violationPaths(t, err)
	for _, path := range []string{"region", "members.0.org", "members.0.note", "members.1.org", "members.1.note"} {
		if !slices.Contains(paths, path) {
			t.Errorf("missing violation %q: %v", path, paths)
		}
	}
}

func TestDeferredPlanNoWorkAndLimits(t *testing.T) {
	t.Parallel()

	for _, raw := range []*fieldmaskpb.FieldMask{maskOf([]string{}), maskOf([]string{"display_name"})} {
		req := &pb.Account{DisplayName: "new"}

		p, err := update.Prepare(raw, req)
		if err != nil || len(p.Operations()) != 0 {
			t.Fatalf("unexpected pending work: %v %v", p, err)
		}

		for _, current := range []proto.Message{nil, &fieldmaskpb.FieldMask{}} {
			got, err := p.Complete(current)
			if err != nil || !reflect.DeepEqual(p.Mask(), got) {
				t.Fatalf("no-op read stored state: %v %v", got, err)
			}
		}
	}

	for _, tc := range []struct {
		paths []string
		opt   update.Option
	}{
		{[]string{"region", "display_name"}, update.WithMaxPaths(1)},
		{[]string{"region"}, update.WithMaxPathLength(2)},
		{[]string{"owner.org"}, update.WithMaxPathDepth(1)},
	} {
		req := storedAccount()
		before := proto.Clone(req)

		_, err := update.Prepare(maskOf(tc.paths), req, tc.opt)
		if !errors.Is(err, fieldmask.ErrLimitExceeded) || !proto.Equal(req, before) {
			t.Fatalf("limit not enforced before mutation: %v", err)
		}
	}
}

// A stateless REQUIRED refusal cannot report additional comparisons requiring
// stored state. Keep the existing early refusal instead of returning a usable
// plan for an invalid request or inventing stored-field diagnostics.
func TestDeferredPlanEarlyRequiredRefusal(t *testing.T) {
	t.Parallel()

	req, stored := storedAccount(), storedAccount()
	req.Owner = nil
	before := proto.Clone(req)
	raw := maskOf([]string{"owner", "owner.org"})
	p, early := update.Prepare(raw, req)

	_, stateful := update.ApplyMask(raw, proto.Clone(before), update.WithCurrent(stored))
	if p != nil || !errors.Is(early, update.ErrBehaviorViolation) || !errors.Is(stateful, update.ErrBehaviorViolation) {
		t.Fatalf("expected existing early REQUIRED refusal: %v / %v", early, stateful)
	}

	if len(violationPaths(t, early)) != 1 || len(violationPaths(t, stateful)) != 2 || !proto.Equal(req, before) {
		t.Fatalf("unexpected early diagnostics or mutation: %v / %v", early, stateful)
	}
}

func TestDeferredPlanConsumers(t *testing.T) {
	t.Parallel()

	for _, entry := range []string{"spec_option", "spec_context", "patch"} {
		for _, operation := range []string{"clear", "equal_list", "changed_list", "removed_map", "equal_atomic"} {
			t.Run(entry+"/"+operation, func(t *testing.T) {
				stored, req := storedAccount(), storedAccount()
				paths := []string{"members"}

				var opts []update.Option

				wantError := false

				switch operation {
				case "clear":
					paths, req.Region, wantError = []string{"region"}, "", true
				case "changed_list":
					req.Members[0].Org, wantError = "changed", true
				case "removed_map":
					paths, req.Delegates, wantError = []string{"delegates"}, nil, true
				case "equal_atomic":
					paths, req.Owner.Note = []string{"owner"}, "updated"

					opts = append(opts, atomicNames("fieldbehaviortest.v1.Owner"))
				case "equal_list":
					req.Members[0].Note = "updated"
				}

				pending, err := update.Prepare(maskOf(paths), req, opts...)
				if err != nil {
					t.Fatal(err)
				}

				spec, err := update.NewSpec(&pb.Account{}, opts...)
				if err != nil {
					t.Fatal(err)
				}

				ctx := update.ContextWithPlan(t.Context(), pending)
				normalized := pending.Mask().ToProtoFieldMask()

				var final []string

				switch entry {
				case "patch":
					store := &accountStore{cur: stored}
					patch := store.patch(t)
					patch.Spec = spec
					_, err = patch.Run(ctx, req, "", normalized)

					if wantError && store.saved != nil {
						t.Fatal("rejected update was saved")
					}

					if !wantError && (store.saved == nil || (operation == "equal_list" && store.saved.Members[0].Note != "updated") ||
						(operation == "equal_atomic" && store.saved.Owner.Note != "updated")) {
						t.Fatalf("deferred mutable siblings were not saved: %v (%v)", store.saved, err)
					}
				default:
					option := update.WithDeferredPlan(pending)
					if entry == "spec_context" {
						option = update.WithPlanContext(ctx)
					}

					var plan update.Plan

					plan, err = spec.Plan(req, normalized, update.WithCurrent(stored), option)
					final = plan.Paths
				}

				if wantError {
					if !errors.Is(err, update.ErrProtectedField) {
						t.Fatalf("deferred violation lost its error identities: %v", err)
					}

					if _, ok := errors.AsType[*update.MaskPathError](err); !ok {
						t.Fatalf("deferred violation lost its plan provenance: %v", err)
					}
				} else if err != nil || (entry != "patch" && !slices.Equal(final, paths)) {
					t.Fatalf("deferred replacement not completed: %v %v", final, err)
				}
			})
		}
	}
}

func TestStripUpdateCompletion(t *testing.T) {
	t.Parallel()

	for _, entry := range []string{"spec", "patch"} {
		for _, value := range []string{"same", "changed", "absent"} {
			t.Run(entry+"/"+value, func(t *testing.T) {
				stored := storedAccount()

				req := &pb.Account{DisplayName: "updated", Region: stored.Region}
				if value == "changed" {
					req.Region = "changed"
				} else if value == "absent" {
					req.Region = ""
				}

				if err := fieldbehavior.StripUpdate(req); err != nil {
					t.Fatal(err)
				}

				var err error

				if entry == "patch" {
					store := &accountStore{cur: stored}
					_, err = store.patch(t).Run(t.Context(), req, "", nil)
				} else {
					spec, specErr := update.NewSpec(&pb.Account{})
					if specErr != nil {
						t.Fatal(specErr)
					}

					_, err = spec.Plan(req, nil, update.WithCurrent(stored))
				}

				if value == "changed" {
					if !errors.Is(err, update.ErrProtectedField) {
						t.Fatalf("populated protected input was not compared: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestDeferredPlanEarlyOneofRefusal(t *testing.T) {
	t.Parallel()

	md := collectionDescriptor(t)
	stored := collectionRoot(md, map[string]map[string]string{"k": {"free": "old"}}, nil, nil)
	req := collectionRoot(md, map[string]map[string]string{"k": {"free": "new"}}, nil, nil)
	raw := maskOf([]string{"items.k.free"})
	_, early := update.Prepare(raw, proto.Clone(req))

	_, stateful := update.ApplyMask(raw, req, update.WithCurrent(stored))
	if !errors.Is(early, update.ErrBehaviorViolation) || stateful != nil {
		t.Fatalf("existing conservative oneof check changed: stateless=%v stateful=%v", early, stateful)
	}
}

// Early REQUIRED refusals cannot know the additional protected-field diagnostics
// of a stateful call. Check this documented boundary without skipping those cases.
func compareEarlyDeferredError(t *testing.T, want, got error) {
	t.Helper()

	if fmt.Sprint(want) == fmt.Sprint(got) {
		compareDeferredError(t, want, got)
		return
	}

	w, wok := errors.AsType[*update.BehaviorError](want)

	g, gok := errors.AsType[*update.BehaviorError](got)
	if errors.Is(want, update.ErrElementCountMismatch) && gok && len(g.Violations) > 0 {
		// Prepare has no stored list length. Its existing early REQUIRED
		// rejection cannot carry a mismatch discovered only with stored state.
		for _, v := range g.Violations {
			if v.Behavior != annotations.FieldBehavior_REQUIRED {
				t.Fatalf("unexpected early refusal before count validation: %v", got)
			}
		}

		return
	}

	if !wok || !gok || len(g.Violations) == 0 {
		t.Fatalf("unexpected early difference: %v / %v", want, got)
	}

	for _, v := range g.Violations {
		if v.Behavior != annotations.FieldBehavior_REQUIRED || !slices.Contains(w.Violations, v) {
			t.Fatalf("early refusal is not a shared REQUIRED violation: %v / %v", want, got)
		}
	}

	for _, v := range w.Violations {
		if !slices.Contains(g.Violations, v) && v.Behavior != annotations.FieldBehavior_IMMUTABLE && v.Behavior != annotations.FieldBehavior_IDENTIFIER {
			t.Fatalf("unexpected stateful-only violation: %v", v)
		}
	}

	t.Logf("documented early REQUIRED boundary: stateless=%v; stateful=%v", got, want)
}

func TestDeferredPlanUnselectedProtectedInput(t *testing.T) {
	t.Parallel()

	checkDeferredEquivalence(t, maskOf([]string{"display_name"}), &pb.Account{DisplayName: "new", Region: "unselected"}, storedAccount())

	md := protectedOnlyDescriptor(t)
	req := dynamicpb.NewMessage(md)
	imm := req.Mutable(md.Fields().ByName("imm")).Message()
	imm.Set(imm.Descriptor().Fields().ByName("code"), protoreflect.ValueOfString("same"))
	checkDeferredEquivalence(t, maskOf([]string{"imm"}), req, proto.Clone(req), update.WithMaxPathDepth(1))
}

func TestDeferredPlanProtectedMessageClear(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Owner: &pb.Owner{Note: "unselected"}}
	stored := &pb.Account{Owner: &pb.Owner{Email: "stored@example.com"}}
	policy := update.WithImmutableFields("owner")
	raw := maskOf([]string{"owner.email"})
	checkDeferredEquivalence(t, raw, req, stored, policy)

	p, err := update.Prepare(raw, req, policy)
	if err != nil {
		t.Fatal(err)
	}

	ops := p.Operations()
	if len(ops) != 1 || ops[0].Path != "owner.email" || ops[0].Intent != update.DeferredClear {
		t.Fatalf("clear below a protected message was lost: %v", ops)
	}
}

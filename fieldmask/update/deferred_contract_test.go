// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// consumePlanSecrets models a service consuming Plan.Secret directly, including
// explicit empty-string writes that select a oneof member. Fixtures use strings.
func consumePlanSecrets(dst, input proto.Message, plan update.Plan) {
	m, src := dst.ProtoReflect(), input.ProtoReflect()

	fields := src.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.StringKind || fd.IsList() || fd.IsMap() {
			continue
		}

		var value *string
		if src.Has(fd) {
			value = new(src.Get(fd).String())
		}

		if secret := plan.Secret(string(fd.Name()), value); secret != nil {
			m.Set(fd, protoreflect.ValueOfString(*secret))
		}
	}
}

func policyPairResultAt(t *testing.T, boundary, specPolicy sourcePolicy, original, stored, prepared proto.Message,
	raw *fieldmaskpb.FieldMask, entry string,
) (proto.Message, error) {
	t.Helper()

	before := proto.Clone(stored)
	req := proto.Clone(original)

	opts := boundary.options()
	if prepared != nil {
		opts = append(opts, update.WithCurrent(prepared))
	}

	pending, err := update.Prepare(raw, req, opts...)
	if err != nil {
		return nil, err
	}

	spec, err := update.NewSpec(req, specPolicy.options()...)
	require.NoError(t, err)
	ctx := update.ContextWithPlan(t.Context(), pending)

	proto.Reset(req) // planning must use the captured input, not this mutation

	writes := 0

	var got proto.Message

	if entry == "Spec" {
		var plan update.Plan

		plan, err = spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithPlanContext(ctx), update.WithCurrent(stored))
		if err == nil {
			got = proto.Clone(stored)

			err = update.MergePaths(got, req, plan.Paths, specPolicy.options()...)
			if err == nil {
				consumePlanSecrets(got, req, plan)
			}
		}
	} else {
		patch := sourcePatch(spec, stored, func(m proto.Message, plan update.Plan) {
			writes++

			consumePlanSecrets(m, req, plan)
			assertPolicyProtectedEqual(t, stored, m, boundary)
			assertPolicyProtectedEqual(t, stored, m, specPolicy)
		})
		got, err = patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask(), update.WithValidateOnly(entry == "ValidateOnly"))
	}

	requireProtoEqual(t, before, stored)

	if err != nil || entry == "ValidateOnly" {
		require.Zero(t, writes)
	}

	if err == nil {
		assertPolicyProtectedEqual(t, stored, got, boundary)
		assertPolicyProtectedEqual(t, stored, got, specPolicy)
	}

	return got, err
}

// oracleBoundaryInput is deliberately separate from the implementation's mask
// helpers. Generated masks have no keyed exclusions; those are covered by exact
// resource regressions in TestDeferredOutputKeyExclusions.
func oracleBoundaryInput(original proto.Message, raw *fieldmaskpb.FieldMask, policy sourcePolicy) (proto.Message, *fieldmaskpb.FieldMask, []int) {
	input := proto.Clone(original)
	clearOracleOutput(input.ProtoReflect(), policy.resolver())

	if raw == nil {
		return input, nil, nil
	}

	out := &fieldmaskpb.FieldMask{}

	var origins []int

	for i, path := range raw.GetPaths() {
		md := original.ProtoReflect().Descriptor()
		ignored := false

		for name := range strings.SplitSeq(path, ".") {
			if name == "*" || md == nil {
				continue
			}

			fd := md.Fields().ByName(protoreflect.Name(name))
			if fd == nil {
				break
			}

			for _, b := range policy.resolver().Resolve(fd) {
				ignored = ignored || b == annotations.FieldBehavior_OUTPUT_ONLY
			}

			md = fd.Message()
		}

		if !ignored {
			out.Paths = append(out.Paths, path)
			origins = append(origins, i)
		}
	}

	return input, out, origins
}

func oneStagePolicyResult(t *testing.T, policy sourcePolicy, input, stored proto.Message, raw *fieldmaskpb.FieldMask,
	entry string,
) (proto.Message, proto.Message, error) {
	t.Helper()

	req := proto.Clone(input)
	spec, err := update.NewSpec(req, policy.options()...)
	require.NoError(t, err)

	if entry == "Spec" {
		plan, err := spec.Plan(req, raw, update.WithCurrent(stored))
		if err != nil {
			return nil, req, err
		}

		got := proto.Clone(stored)
		if err := update.MergePaths(got, req, plan.Paths, policy.options()...); err != nil {
			return nil, req, err
		}

		consumePlanSecrets(got, req, plan)

		return got, req, nil
	}

	patch := sourcePatch(spec, stored, func(m proto.Message, plan update.Plan) { consumePlanSecrets(m, req, plan) })
	got, err := patch.Run(t.Context(), req, "", raw, update.WithValidateOnly(entry == "ValidateOnly"))

	return got, req, err
}

// assertDeferredContract checks acceptance using independent per-policy value
// projections of the complete Spec write, never using the deferred guard itself
// as the oracle. Whenever that write preserves boundary protection, equality to
// one-stage planning is required (including all pairs with no extra protection).
func assertDeferredContract(t *testing.T, boundary, specPolicy sourcePolicy, original, stored, prepared proto.Message,
	raw *fieldmaskpb.FieldMask, entry string,
) {
	t.Helper()

	prepareOpts := boundary.options()
	if prepared != nil {
		prepareOpts = append(prepareOpts, update.WithCurrent(prepared))
	}

	_, early := update.Prepare(raw, proto.Clone(original), prepareOpts...)

	got, err := policyPairResultAt(t, boundary, specPolicy, original, stored, prepared, raw, entry)
	if early != nil {
		compareDeferredError(t, early, err)
		return // the contract applies only after a plan has been bound
	}

	_, boundaryErr := update.ApplyMask(raw, proto.Clone(original), append(boundary.options(), update.WithCurrent(stored))...)
	if boundaryErr != nil {
		require.Error(t, err, "the boundary must compare the planning snapshot")
		return
	}

	input, mask, origins := oracleBoundaryInput(original, raw, boundary)

	want, _, specErr := oneStagePolicyResult(t, specPolicy, input, stored, mask, entry)
	if specErr != nil {
		compareSanitizedRefusal(t, specErr, err, raw, origins)
		return
	}

	// Both public consumers must be safe, even when validate_only skips Save.
	for _, consumer := range []string{"Spec", "Patch"} {
		after, _, writeErr := oneStagePolicyResult(t, specPolicy, input, stored, mask, consumer)
		if writeErr != nil || !policyProtectedEqual(stored, after, boundary) {
			require.Error(t, err, "complete Spec write violates the boundary")

			if writeErr == nil {
				require.ErrorIs(t, err, update.ErrProtectedField)
			}

			return
		}
	}

	require.NoError(t, err)
	requireProtoEqual(t, want, got)

	// Compare the bound request and exact paths as well as the stored result.
	req := proto.Clone(original)
	pending, err := update.Prepare(raw, req, prepareOpts...)
	require.NoError(t, err)
	spec, err := update.NewSpec(req, specPolicy.options()...)
	require.NoError(t, err)

	one := proto.Clone(input)
	wantPlan, wantErr := spec.Plan(one, mask, update.WithCurrent(stored))
	plan, planErr := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithDeferredPlan(pending), update.WithCurrent(stored))
	compareDeferredError(t, wantErr, planErr)
	require.Equal(t, wantPlan.Paths, plan.Paths)
	requireProtoEqual(t, one, req)
}

// compareSanitizedRefusal checks the one-stage semantics while locating a
// refusal in the original fixture. The independent boundary oracle records the
// retained raw positions; it does not call production sanitization or translation.
func compareSanitizedRefusal(t *testing.T, want, got error, raw *fieldmaskpb.FieldMask, origins []int) {
	t.Helper()

	w, wok := errors.AsType[*update.MaskPathError](want)

	g, gok := errors.AsType[*update.MaskPathError](got)
	if !wok || w.Index < 0 {
		compareDeferredError(t, want, got)
		return
	}

	require.True(t, gok)
	require.Equal(t, origins[w.Index], g.Index)
	require.Equal(t, raw.GetPaths()[origins[w.Index]], g.Path)
	require.Equal(t, w.Field, g.Field)
	compareDeferredError(t, w.Err, g.Err)

	wf, wok := errors.AsType[*coreerrs.FieldError](want)
	gf, gok := errors.AsType[*coreerrs.FieldError](got)
	require.True(t, wok && gok)

	wantField := wf.Field
	if wantField == fmt.Sprintf("updateMask.paths[%d]", w.Index) {
		wantField = fmt.Sprintf("updateMask.paths[%d]", origins[w.Index])
	}

	require.Equal(t, wantField, gf.Field)
	require.Equal(t, wf.Reason, gf.Reason)
}

func assertBoundaryRefusal(t *testing.T, err error, field, entry string) {
	t.Helper()

	require.ErrorIs(t, err, update.ErrProtectedField)

	var (
		mp *update.MaskPathError
		fe *coreerrs.FieldError
	)

	require.ErrorAs(t, err, &mp)
	require.Equal(t, field, mp.Field)
	require.ErrorAs(t, err, &fe)

	if entry != "Spec" {
		require.Equal(t, "resource."+field, fe.Field)
	}

	_, aggregate := errors.AsType[*update.BehaviorError](err)
	_, strict := errors.AsType[*update.StrictError](err)

	require.False(t, aggregate)
	require.False(t, strict)
}

func TestDeferredSecretOneofEffect(t *testing.T) {
	md := collectionDescriptor(t).Fields().ByName("list").Message()

	for _, named := range []bool{false, true} {
		for _, supplied := range []bool{false, true} {
			for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
				t.Run(fmt.Sprintf("named=%t/supplied=%t/%s", named, supplied, entry), func(t *testing.T) {
					stored, request := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
					stored.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString("keep"))
					stored.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("old"))
					request.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("new"))

					if supplied {
						request.Set(md.Fields().ByName("free"), protoreflect.ValueOfString("new"))
					}

					paths := []string{"note"}
					if named {
						paths = append(paths, "free")
						if supplied {
							paths = append(paths, "pinned") // defer the protected comparison at Prepare
						}
					}

					// R29-01 is the supplied secret outside the mask. A named
					// unset secret also selects free when Save consumes its clear.
					got, err := policyPairResultAt(t, sourcePolicy{immutable: []string{"pinned"}}, sourcePolicy{secret: "free"},
						request, stored, nil, maskOf(paths), entry)
					if supplied || named {
						assertBoundaryRefusal(t, err, "pinned", entry)
						return
					}

					require.NoError(t, err)

					want := proto.Clone(stored)
					want.ProtoReflect().Set(md.Fields().ByName("note"), protoreflect.ValueOfString("new"))
					requireProtoEqual(t, want, got)
				})
			}
		}
	}
}

func TestDeferredUsesPlanningSnapshot(t *testing.T) {
	prepared := &descriptorpb.FileDescriptorProto{Name: new("a"), Package: new("old")}
	stored := &descriptorpb.FileDescriptorProto{Name: new("b"), Package: new("old")}
	request := &descriptorpb.FileDescriptorProto{Name: new("a"), Package: new("new")}

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			_, err := policyPairResultAt(t, sourcePolicy{immutable: []string{"name"}}, sourcePolicy{}, request, stored, prepared,
				maskOf([]string{"name", "package"}), entry)
			assertBoundaryRefusal(t, err, "name", entry)

			var mp *update.MaskPathError

			require.ErrorAs(t, err, &mp)
			require.Equal(t, 0, mp.Index)
			require.Equal(t, "name", mp.Path)
			requireProtoEqual(t, &descriptorpb.FileDescriptorProto{Name: new("a"), Package: new("old")}, prepared)
		})
	}
}

func TestDeferredDifferentAtomicPolicies(t *testing.T) {
	boundary := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"fieldbehaviortest.v1.Owner.email": annotations.FieldBehavior_IMMUTABLE,
	}}
	specPolicy := sourcePolicy{atomic: "fieldbehaviortest.v1.Owner"}
	stored := &pb.Account{Owner: &pb.Owner{Email: "keep", Note: "old"}}
	request := &pb.Account{Owner: &pb.Owner{Email: "keep", Note: "new"}}

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			got, err := policyPairResult(t, boundary, specPolicy, request, stored, maskOf([]string{"owner"}), entry)
			require.NoError(t, err)
			requireProtoEqual(t, request, got)
			assertDeferredContract(t, boundary, specPolicy, request, stored, nil, maskOf([]string{"owner"}), entry)
		})
	}
}

func runDeferredContractGenerator(t *testing.T, rng *rand.Rand) {
	md := collectionDescriptor(t).Fields().ByName("list").Message()

	policies := []sourcePolicy{
		{name: "mutable"}, {name: "secret-free", secret: "free"},
		{name: "immutable-pinned", immutable: []string{"pinned"}},
		{name: "identity-pinned", identity: "pinned"}, {name: "annotated-pinned", annotated: "pinned"},
		{name: "secret-and-protected", secret: "free", immutable: []string{"pinned"}},
		{name: "ignored-free", ignored: "free"}, {name: "immutable-free", immutable: []string{"free"}},
	}
	for boundaryIndex, boundary := range policies {
		for specIndex, specPolicy := range policies {
			for variant := range 4 {
				stored, request := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
				stored.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString(fmt.Sprintf("keep-%x", rng.Uint64())))
				stored.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("old"))
				request.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("new"))

				if variant < 2 {
					request.Set(md.Fields().ByName("free"), protoreflect.ValueOfString("secret"))
				}

				paths := []string{"note"}
				if variant%2 == 1 {
					paths = append(paths, "free")
				}

				for entryIndex, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
					// Cover consumer/policy and consumer/variant pairs without the full product.
					if entryIndex != (boundaryIndex+specIndex+variant)%3 {
						continue
					}

					t.Run(fmt.Sprintf("secret/%s/%s/%d/%s", boundary.name, specPolicy.name, variant, entry), func(t *testing.T) {
						assertDeferredContract(t, boundary, specPolicy, request, stored, nil, maskOf(paths), entry)
					})
				}
			}
		}
	}

	policies = []sourcePolicy{
		{name: "expandable"}, {name: "atomic", atomic: "fieldbehaviortest.v1.Owner"},
		{name: "protected-email", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"fieldbehaviortest.v1.Owner.email": annotations.FieldBehavior_IMMUTABLE,
		}},
		{name: "atomic-protected-email", atomic: "fieldbehaviortest.v1.Owner", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"fieldbehaviortest.v1.Owner.email": annotations.FieldBehavior_IMMUTABLE,
		}},
	}
	for boundaryIndex, boundary := range policies {
		for specIndex, specPolicy := range policies {
			for variant := range 3 {
				prepared := &pb.Account{Owner: &pb.Owner{Email: fmt.Sprintf("keep-%x", rng.Uint64()), Note: "old"}}
				stored, request := proto.CloneOf(prepared), proto.CloneOf(prepared)

				request.Owner.Note = "new"
				if variant == 1 {
					stored.Owner.Email = "changed-since-prepare"
				} else if variant == 2 {
					stored.Owner.Note = "changed-since-prepare"
				}

				for entryIndex, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
					// Cover consumer/policy and consumer/variant pairs without the full product.
					if entryIndex != (boundaryIndex+specIndex+variant)%3 {
						continue
					}

					t.Run(fmt.Sprintf("atomic-snapshot/%s/%s/%d/%s", boundary.name, specPolicy.name, variant, entry), func(t *testing.T) {
						assertDeferredContract(t, boundary, specPolicy, request, stored, prepared, maskOf([]string{"owner"}), entry)
					})
				}
			}
		}
	}
}

func TestDeferredOutputOnlyMaskSuppressesSecrets(t *testing.T) {
	md := collectionDescriptor(t).Fields().ByName("list").Message()
	stored, request := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	stored.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString("keep"))
	stored.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("old"))
	request.Set(md.Fields().ByName("free"), protoreflect.ValueOfString("secret"))
	request.Set(md.Fields().ByName("note"), protoreflect.ValueOfString("ignored"))

	boundary := sourcePolicy{ignored: "note"}
	specPolicy := sourcePolicy{secret: "free"}

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			got, err := policyPairResult(t, boundary, specPolicy, request, stored, maskOf([]string{"note"}), entry)
			require.NoError(t, err)
			requireProtoEqual(t, stored, got)
			assertDeferredContract(t, boundary, specPolicy, request, stored, nil, maskOf([]string{"note"}), entry)
		})
	}
}

func TestDeferredBoundaryRefusalBeforeAtomicSpec(t *testing.T) {
	boundary := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"fieldbehaviortest.v1.Owner.email": annotations.FieldBehavior_IMMUTABLE,
	}}
	specPolicy := sourcePolicy{atomic: "fieldbehaviortest.v1.Owner"}
	stored := &pb.Account{Owner: &pb.Owner{Email: "keep"}}
	request := &pb.Account{Owner: &pb.Owner{Email: "changed"}}

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			_, err := policyPairResult(t, boundary, specPolicy, request, stored, maskOf([]string{"owner.email"}), entry)
			assertBoundaryRefusal(t, err, "owner.email", entry)

			var mp *update.MaskPathError

			require.ErrorAs(t, err, &mp)
			require.Equal(t, 0, mp.Index)
			require.Equal(t, "owner.email", mp.Path)
		})
	}
}

func TestDeferredNestedOutputExclusions(t *testing.T) {
	file := protodesc.ToFileDescriptorProto(collectionDescriptor(t).ParentFile())
	file.MessageType[1].Field = append(file.MessageType[1].Field, &descriptorpb.FieldDescriptorProto{
		Name: new("children"), Number: new(int32(4)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), TypeName: new(".colltest.Root"),
	})
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	md := fd.Messages().ByName("Root")
	parse := func(text string) proto.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}
	stored := parse(`items { key:"k" value { note:"old" children { items { key:"x" value { note:"old" free:"old" } } } } }
		items { key:"j" value { note:"old" } }`)
	request := parse(`items { key:"k" value { note:"new" children { items { key:"x" value { note:"new" free:"new" } } } } }
		items { key:"j" value { note:"new" } }`)
	want := parse(`items { key:"k" value { note:"old" children { items { key:"x" value { note:"old" free:"old" } } } } }
		items { key:"j" value { note:"new" } }`)
	paths := []string{"items.*.note", "items.k.children.items.*.note", "items.k.children.items.x.free"}

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			got, err := policyPairResult(t, sourcePolicy{ignored: "free"}, sourcePolicy{}, request, stored, maskOf(paths), entry)
			require.NoError(t, err)
			requireProtoEqual(t, want, got)
		})
	}
}

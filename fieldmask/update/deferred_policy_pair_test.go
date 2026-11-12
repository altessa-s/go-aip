// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func assertPolicyProtectedEqual(t *testing.T, stored, after proto.Message, policy sourcePolicy) {
	t.Helper()

	if !policyProtectedEqual(stored, after, policy) {
		t.Fatalf("policy %q protected values changed: stored=%v after=%v", policy.name, stored, after)
	}
}

func policyProtectedEqual(stored, after proto.Message, policy sourcePolicy) bool {
	resolve := policy.resolver()
	top := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		b := resolve.Resolve(fd)
		if slices.Contains(b, annotations.FieldBehavior_OUTPUT_ONLY) {
			return b
		}

		if policy.protects(string(fd.Name())) {
			return append(slices.Clone(b), annotations.FieldBehavior_IMMUTABLE)
		}

		return b
	})

	return oracleProtectedEqual(after.ProtoReflect(), stored.ProtoReflect(), resolve, top)
}

// policyPairResult exercises the public deferred merge consumers, checking the
// direct protected-value oracle on every accepted result and never mutating stored.
func policyPairResult(t *testing.T, boundary, specPolicy sourcePolicy, original, stored proto.Message,
	raw *fieldmaskpb.FieldMask, entry string,
) (proto.Message, error) {
	t.Helper()

	return policyPairResultAt(t, boundary, specPolicy, original, stored, nil, raw, entry)
}

func TestDeferredDifferingOneofPolicies(t *testing.T) {
	md := collectionDescriptor(t).Fields().ByName("list").Message()
	stored, request := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	stored.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString("keep"))
	request.Set(md.Fields().ByName("free"), protoreflect.ValueOfString("new"))

	for _, identity := range []bool{false, true} {
		boundary := sourcePolicy{name: "boundary", immutable: []string{"pinned"}, ignored: "free"}
		if identity {
			boundary.immutable, boundary.identity = nil, "pinned"
		}

		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(fmt.Sprintf("ignored-write/identity=%t/%s", identity, entry), func(t *testing.T) {
				// R28-01: the boundary removes the only raw path as OUTPUT_ONLY,
				// so K0 is explicitly empty and pinned must survive exactly.
				got, err := policyPairResult(t, boundary, sourcePolicy{ignored: "pinned"}, request, stored,
					maskOf([]string{"free"}), entry)
				require.NoError(t, err)
				requireProtoEqual(t, stored, got)
			})
		}
	}

	for _, boundaryProtects := range []bool{false, true} {
		boundary, specPolicy := sourcePolicy{ignored: "pinned"}, sourcePolicy{immutable: []string{"pinned"}}
		if boundaryProtects {
			boundary, specPolicy = specPolicy, boundary
		}

		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(fmt.Sprintf("refused-switch/boundary=%t/%s", boundaryProtects, entry), func(t *testing.T) {
				_, err := policyPairResult(t, boundary, specPolicy, request, stored, maskOf([]string{"pinned", "free"}), entry)
				require.ErrorIs(t, err, update.ErrProtectedField)

				var fieldErr *coreerrs.FieldError

				require.ErrorAs(t, err, &fieldErr)

				want := "resource.pinned"
				if entry == "Spec" {
					want = "updateMask.paths[0]"
					if !boundaryProtects {
						// The boundary ignores the pinned path. Spec sees only
						// free, which does not cover the refused sibling pinned.
						want = "updateMask"
					}
				}

				require.Equal(t, want, fieldErr.Field)

				var pathErr *update.MaskPathError

				require.ErrorAs(t, err, &pathErr)
				require.Equal(t, "pinned", pathErr.Field)

				index, path := -1, ""
				if boundaryProtects {
					index, path = 0, "pinned"
				}

				require.Equal(t, index, pathErr.Index)
				require.Equal(t, path, pathErr.Path)
			})
		}
	}
}

func TestDeferredIndependentRefusalPrecedence(t *testing.T) {
	stored := &descriptorpb.FileDescriptorProto{Name: new("old"), Package: new("old")}
	request := &descriptorpb.FileDescriptorProto{Name: new("new"), Package: new("new")}

	for _, specPolicy := range []sourcePolicy{{name: "immutable", immutable: []string{"name"}}, {name: "routing", identity: "name"}} {
		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(specPolicy.name+"/"+entry, func(t *testing.T) {
				_, err := policyPairResult(t, sourcePolicy{immutable: []string{"package"}}, specPolicy,
					request, stored, maskOf([]string{"name", "package"}), entry)
				require.ErrorIs(t, err, update.ErrProtectedField)

				var fieldErr *coreerrs.FieldError

				require.ErrorAs(t, err, &fieldErr)

				want := "resource.package"
				if entry == "Spec" {
					want = "updateMask.paths[1]"
				}

				require.Equal(t, want, fieldErr.Field)
			})
		}
	}
}

func TestDeferredProtectedIgnoredDescendant(t *testing.T) {
	boundary := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"google.protobuf.DescriptorProto.options":   annotations.FieldBehavior_IMMUTABLE,
		"google.protobuf.MessageOptions.deprecated": annotations.FieldBehavior_OUTPUT_ONLY,
	}}
	stored := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{{Name: new("old"),
		Options: &descriptorpb.MessageOptions{Deprecated: new(true)}}}}
	request := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{{Name: new("new"),
		Options: &descriptorpb.MessageOptions{}}}}
	want := proto.CloneOf(stored)
	want.MessageType[0].Name = new("new")
	want.MessageType[0].Options.Deprecated = nil

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			got, err := policyPairResult(t, boundary, sourcePolicy{}, request, stored, maskOf([]string{"message_type"}), entry)
			require.NoError(t, err)
			requireProtoEqual(t, want, got)
		})
	}
}

func TestDeferredOutputKeyExclusions(t *testing.T) {
	md := collectionDescriptor(t)
	stored := collectionRoot(md, map[string]map[string]string{
		"k": {"note": "old", "free": "old"}, "j": {"note": "old"},
	}, nil, nil)
	request := collectionRoot(md, map[string]map[string]string{
		"k": {"note": "new", "free": "new"}, "j": {"note": "new"},
	}, nil, nil)
	want := collectionRoot(md, map[string]map[string]string{
		"k": {"note": "old", "free": "old"}, "j": {"note": "new"},
	}, nil, nil)

	for _, paths := range [][]string{
		{"items.*.note", "items.k.free", "items.k.pinned"}, // the Spec ignores the remaining k path
		{"items.*.note", "items.k.free"},                   // boundary pins *, Spec retains it
	} {
		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(fmt.Sprintf("%v/%s", paths, entry), func(t *testing.T) {
				got, err := policyPairResult(t, sourcePolicy{ignored: "free"}, sourcePolicy{ignored: "pinned"},
					request, stored, maskOf(paths), entry)
				require.NoError(t, err)
				requireProtoEqual(t, want, got)
			})
		}
	}
}

func TestDeferredSpecOwnsCoveringPath(t *testing.T) {
	stored := &pb.Account{Owner: &pb.Owner{Email: "keep", Note: "old"}}
	original := &pb.Account{Owner: &pb.Owner{Email: "keep", Note: "new"}}
	protected := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "email" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	}))

	for _, boundaryNarrower := range []bool{false, true} {
		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(fmt.Sprintf("boundary-narrower=%t/%s", boundaryNarrower, entry), func(t *testing.T) {
				boundary, specOptions := sourcePolicy{}.options(), []update.Option{protected}
				if boundaryNarrower {
					boundary, specOptions = specOptions, boundary
				}

				req := proto.CloneOf(original)
				pending, err := update.Prepare(maskOf([]string{"owner"}), req, boundary...)
				require.NoError(t, err)
				spec, err := update.NewSpec(req, specOptions...)
				require.NoError(t, err)
				ctx := update.ContextWithPlan(t.Context(), pending)

				var got proto.Message

				if entry == "Spec" {
					plan, err := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithPlanContext(ctx), update.WithCurrent(stored))
					require.NoError(t, err)

					if boundaryNarrower {
						require.Equal(t, []string{"owner"}, plan.Paths)
					} else {
						require.NotContains(t, plan.Paths, "owner")
						require.NotContains(t, plan.Paths, "owner.email")
						require.Contains(t, plan.Paths, "owner.note")
					}

					got = proto.Clone(stored)
					require.NoError(t, update.MergePaths(got, req, plan.Paths))
				} else {
					writes := 0
					patch := sourcePatch(spec, stored, func(m proto.Message, _ update.Plan) {
						writes++

						requireProtoEqual(t, original, m)
					})
					got, err = patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask(), update.WithValidateOnly(entry == "ValidateOnly"))
					require.NoError(t, err)

					if entry == "ValidateOnly" {
						require.Zero(t, writes)
					}
				}

				requireProtoEqual(t, original, got)
			})
		}
	}
}

// runNestedPolicySourceGenerator extends the original seeded Cartesian generator
// to conflicting ancestor/descendant classifications and oneof members. The
// boundary refusal, one-stage Spec result and independent protected-value
// projections determine acceptance and the exact expected final resource.
func runNestedPolicySourceGenerator(t *testing.T, rng *rand.Rand) {
	policies := []sourcePolicy{
		{name: "mutable"},
		{name: "immutable-options-ignore-deprecated", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"google.protobuf.DescriptorProto.options":   annotations.FieldBehavior_IMMUTABLE,
			"google.protobuf.MessageOptions.deprecated": annotations.FieldBehavior_OUTPUT_ONLY,
		}},
		{name: "immutable-options", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"google.protobuf.DescriptorProto.options": annotations.FieldBehavior_IMMUTABLE,
		}},
		{name: "ignored-options", ignored: "options"},
		{name: "immutable-deprecated", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"google.protobuf.MessageOptions.deprecated": annotations.FieldBehavior_IMMUTABLE,
		}},
		{name: "immutable-map-entry", fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"google.protobuf.MessageOptions.map_entry": annotations.FieldBehavior_IMMUTABLE,
		}},
	}
	for boundaryIndex, boundary := range policies {
		for specIndex, specPolicy := range policies {
			for variant := range 4 {
				stored := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{{Name: new("old"),
					Options: &descriptorpb.MessageOptions{Deprecated: new(true), MapEntry: new(false)}}}}
				request := proto.CloneOf(stored)
				request.MessageType[0].Name = new(fmt.Sprintf("new-%x", rng.Uint64()))

				switch variant {
				case 1:
					request.MessageType[0].Options.Deprecated = nil
				case 2:
					request.MessageType[0].Options.MapEntry = new(true)
				case 3:
					request.MessageType[0].Options = nil
				}

				for entryIndex, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
					// Cover consumer/policy and consumer/variant pairs without the full product.
					if entryIndex != (boundaryIndex+specIndex+variant)%3 {
						continue
					}

					t.Run(fmt.Sprintf("replacement/%s/%s/%d/%s", boundary.name, specPolicy.name, variant, entry), func(t *testing.T) {
						assertIndependentPolicyPair(t, boundary, specPolicy, request, stored, maskOf([]string{"message_type"}), entry)
					})
				}
			}
		}
	}

	md := collectionDescriptor(t).Fields().ByName("list").Message()

	policies = []sourcePolicy{{name: "mutable"}, {name: "immutable", immutable: []string{"pinned"}},
		{name: "identifier", annotated: "pinned"}, {name: "ignored-pinned", ignored: "pinned"}, {name: "ignored-free", ignored: "free"},
		{name: "immutable-ignore-free", immutable: []string{"pinned"}, ignored: "free"}}
	for boundaryIndex, boundary := range policies {
		for specIndex, specPolicy := range policies {
			for variant := range 4 {
				stored, request := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
				value := fmt.Sprintf("keep-%x", rng.Uint64())
				stored.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString(value))

				switch variant {
				case 0:
					request.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString(value))
				case 1:
					request.Set(md.Fields().ByName("pinned"), protoreflect.ValueOfString("changed"))
				case 2:
					request.Set(md.Fields().ByName("free"), protoreflect.ValueOfString("new"))
				}

				for entryIndex, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
					// Cover consumer/policy and consumer/variant pairs without the full product.
					if entryIndex != (boundaryIndex+specIndex+variant)%3 {
						continue
					}

					t.Run(fmt.Sprintf("oneof/%s/%s/%d/%s", boundary.name, specPolicy.name, variant, entry), func(t *testing.T) {
						assertIndependentPolicyPair(t, boundary, specPolicy, request, stored, maskOf([]string{"pinned", "free"}), entry)
					})
				}
			}
		}
	}
}

func assertIndependentPolicyPair(t *testing.T, boundary, specPolicy sourcePolicy, request, stored proto.Message,
	raw *fieldmaskpb.FieldMask, entry string,
) {
	t.Helper()

	assertDeferredContract(t, boundary, specPolicy, request, stored, nil, raw, entry)
}

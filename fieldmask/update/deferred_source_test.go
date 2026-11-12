// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// assertMergeSource checks the values each returned path actually writes, with
// identical destination shapes for collection sub-paths. Fixtures using this
// exact invariant have no OUTPUT_ONLY descendants within whole replacements;
// those intentionally sanitized values have separate preservation assertions.
func assertMergeSource(t *testing.T, original, source, stored proto.Message, paths []string, opts ...update.Option) {
	t.Helper()

	for _, path := range paths {
		want, got := proto.Clone(stored), proto.Clone(stored)
		require.NoError(t, update.MergePaths(want, original, []string{path}, opts...))
		require.NoError(t, update.MergePaths(got, source, []string{path}, opts...))

		if !proto.Equal(want, got) {
			t.Fatalf("merge source lost client value at %q: original=%v source=%v", path, original, source)
		}
	}
}

func TestDeferredBoundaryEqualImmutableSource(t *testing.T) {
	req := &descriptorpb.FileDescriptorProto{Name: new("keep"), Package: new("new")}
	original := proto.Clone(req)
	stored := &descriptorpb.FileDescriptorProto{Name: new("keep"), Package: new("old")}
	pending, err := update.Prepare(maskOf([]string{"name", "package"}), req, update.WithImmutableFields("name"))
	require.NoError(t, err)
	spec, err := update.NewSpec(req)
	require.NoError(t, err)

	plan, err := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithDeferredPlan(pending), update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"name", "package"}, plan.Paths)
	assertMergeSource(t, original, req, stored, plan.Paths)
	require.NoError(t, update.MergePaths(stored, req, plan.Paths))
	requireProtoEqual(t, original, stored)
}

type sourcePolicy struct {
	name      string
	immutable []string
	identity  string // policy-only routing identity
	annotated string // IDENTIFIER annotation
	ignored   string // OUTPUT_ONLY under this policy
	skip      string // normalization-only implied skip
	secret    string // top-level INPUT_ONLY
	atomic    protoreflect.FullName
	fields    map[protoreflect.FullName]annotations.FieldBehavior
}

func (p sourcePolicy) resolver() fieldbehavior.Resolver {
	return fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if b, ok := p.fields[fd.FullName()]; ok {
			return []annotations.FieldBehavior{b}
		}

		switch string(fd.Name()) {
		case p.annotated:
			return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}
		case p.secret:
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
		case p.ignored:
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		default:
			return nil
		}
	})
}

func (p sourcePolicy) options() []update.Option {
	opts := []update.Option{update.WithIdentityFields(), update.WithImmutableFields(p.immutable...), update.WithResolver(p.resolver())}
	if p.identity != "" {
		opts = append(opts, update.WithIdentityFields(p.identity))
	}

	if p.skip != "" {
		opts = append(opts, update.WithImpliedSkipFields(p.skip))
	}

	if p.atomic != "" {
		opts = append(opts, atomicNames(p.atomic))
	}

	return opts
}

func (p sourcePolicy) protects(name string) bool {
	return p.ignored != name && (slices.Contains(p.immutable, name) || p.identity == name || p.annotated == name)
}

// TestDeferredPolicySourceGenerator exercises the Cartesian policy pairs with
// seeded values and explicit equal/change/clear cases. It checks final resources,
// rather than treating matching masks or inspection of cases as behavioral proof.
func TestDeferredPolicySourceGenerator(t *testing.T) {
	policies := []sourcePolicy{
		{name: "mutable"}, {name: "immutable-name", immutable: []string{"name"}},
		{name: "immutable-package", immutable: []string{"package"}},
		{name: "immutable-both", immutable: []string{"name", "package"}},
		{name: "identifier-name", annotated: "name"}, {name: "routing-name", identity: "name"},
		{name: "ignored-name", ignored: "name"}, {name: "skip-package", skip: "package"},
	}
	rng := rand.New(rand.NewPCG(203, 28))

	t.Run("nested-and-oneof", func(t *testing.T) { runNestedPolicySourceGenerator(t, rng) })
	t.Run("secrets-atomic-snapshots", func(t *testing.T) { runDeferredContractGenerator(t, rng) })

	for boundaryIndex, boundary := range policies {
		for specIndex, specPolicy := range policies {
			for variant := range 4 {
				old := fmt.Sprintf("stored-%x", rng.Uint64())
				request := &descriptorpb.FileDescriptorProto{Name: new(old), Package: new(old), Syntax: new("new")}
				stored := &descriptorpb.FileDescriptorProto{Name: new(old), Package: new(old), Syntax: new("old")}

				switch variant {
				case 1:
					request.Name = new("changed")
				case 2:
					request.Package = new("changed")
				case 3:
					request.Name, request.Package = nil, nil
				}

				for _, mode := range []string{"explicit", "implied", "star"} {
					for entryIndex, entry := range []string{"Complete", "Spec", "SpecDeferred", "Patch", "PatchDeferred", "ValidateDeferred"} {
						// Rotate consumers while retaining every policy pair, value and mask mode.
						if entryIndex != (boundaryIndex+specIndex+variant)%6 {
							continue
						}

						t.Run(fmt.Sprintf("%s/%s/%d/%s/%s", boundary.name, specPolicy.name, variant, mode, entry), func(t *testing.T) {
							runPolicySourceCase(t, boundary, specPolicy, request, stored, mode, entry)
						})
					}
				}
			}
		}
	}
}

func runPolicySourceCase(t *testing.T, boundary, specPolicy sourcePolicy, original, stored *descriptorpb.FileDescriptorProto, mode, entry string) {
	t.Helper()

	raw := maskOf([]string{"name", "package", "syntax"})
	if mode == "implied" {
		raw = nil
	} else if mode == "star" {
		raw = maskOf([]string{"*"})
	}

	deferred := entry == "SpecDeferred" || entry == "PatchDeferred" || entry == "ValidateDeferred"
	if deferred {
		consumer := "Spec"
		if entry == "PatchDeferred" {
			consumer = "Patch"
		} else if entry == "ValidateDeferred" {
			consumer = "ValidateOnly"
		}

		assertIndependentPolicyPair(t, boundary, specPolicy, original, stored, raw, consumer)

		return
	}

	active := []sourcePolicy{specPolicy}
	if entry == "Complete" {
		active = []sourcePolicy{boundary}
	}

	want := proto.CloneOf(stored)
	want.Syntax = original.Syntax
	refused := false

	for _, name := range []string{"name", "package"} {
		fd := stored.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
		selected := mode != "implied" || original.ProtoReflect().Has(fd)

		protected := false
		for _, policy := range active {
			protected = protected || policy.protects(name)
			// Policy-only routing input is compared only when explicitly named.
			compare := selected && (policy.identity != name || mode == "explicit")
			if compare && policy.protects(name) && original.ProtoReflect().Get(fd).String() != stored.ProtoReflect().Get(fd).String() {
				refused = true
			}
		}

		if entry != "Complete" && mode == "explicit" && specPolicy.identity == name {
			refused = true // Spec's documented explicit routing-path refusal
		}

		ignored := specPolicy.ignored == name || (mode == "implied" && specPolicy.skip == name)
		if entry == "Complete" {
			ignored = boundary.ignored == name || (mode == "implied" && boundary.skip == name)
		}

		if selected && !protected && !ignored {
			if original.ProtoReflect().Has(fd) {
				want.ProtoReflect().Set(fd, original.ProtoReflect().Get(fd))
			} else {
				want.ProtoReflect().Clear(fd)
			}
		}
	}

	req := proto.CloneOf(original)
	ctx := t.Context()

	var pending *update.DeferredPlan

	if entry == "Complete" {
		var err error

		pending, err = update.Prepare(raw, req, boundary.options()...)
		require.NoError(t, err)

		ctx = update.ContextWithPlan(ctx, pending)
		// Neither subsequent request mutation nor a rewritten mask changes input.
		req.Name, req.Package, req.Syntax = new("later"), new("later"), new("later")
		raw = pending.Mask().ToProtoFieldMask()
	}

	spec, err := update.NewSpec(req, specPolicy.options()...)
	require.NoError(t, err)

	var (
		paths []string
		got   proto.Message
	)

	writes := 0

	switch entry {
	case "Complete":
		mask, completeErr := pending.Complete(stored)
		err = completeErr
		paths = mask.ToPaths()
	case "Spec":
		plan, planErr := spec.Plan(req, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
		err, paths = planErr, plan.Paths
	default:
		patch := sourcePatch(spec, stored, func(m proto.Message, plan update.Plan) {
			writes++

			assertMergeSource(t, original, req, stored, plan.Paths, specPolicy.options()...)
			requireProtoEqual(t, want, m)
		})
		got, err = patch.Run(ctx, req, "", raw)
	}

	if refused {
		requireProtectedRefusal(t, err, entry)
		require.Zero(t, writes)

		return
	}

	require.NoError(t, err)

	if got == nil {
		opts := specPolicy.options()
		if entry == "Complete" {
			opts = boundary.options()
		}

		assertMergeSource(t, original, req, stored, paths, opts...)
		got = proto.Clone(stored)
		require.NoError(t, update.MergePaths(got, req, paths, opts...))
	} else {
		require.Equal(t, 1, writes)
	}

	requireProtoEqual(t, want, got)

	for _, policy := range active {
		assertPolicyProtectedEqual(t, stored, got, policy)
	}
}

func sourcePatch(spec *update.Spec, stored proto.Message, save func(proto.Message, update.Plan)) update.Patch[proto.Message, proto.Message, *int64] {
	return update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
		Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
		Read: func(context.Context) (proto.Message, error) {
			if stored == nil {
				return nil, coreerrs.ErrNotFound
			}

			return stored, nil
		},
		ToProto: func(m proto.Message) proto.Message { return proto.Clone(m) },
		Save: func(_ context.Context, m proto.Message, plan update.Plan, _ *int64) (proto.Message, error) {
			save(m, plan)
			return m, nil
		}}
}

func TestDeferredCreateAndSecretSource(t *testing.T) {
	secret := sourcePolicy{name: "secret"}.options()
	secret = append(secret, update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "name" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
		}

		return nil
	})))

	for _, create := range []bool{false, true} {
		for _, validate := range []bool{false, true} {
			for _, present := range []bool{false, true} {
				t.Run(fmt.Sprintf("create=%t/validate=%t/present=%t", create, validate, present), func(t *testing.T) {
					original := &descriptorpb.FileDescriptorProto{Package: new("new")}
					if present {
						original.Name = new("client-secret")
					}

					req := proto.CloneOf(original)
					want := &descriptorpb.FileDescriptorProto{Package: new("new")}
					// The boundary removes the secret, even when explicitly selected.
					pending, err := update.Prepare(maskOf([]string{"name", "package"}), req, sourcePolicy{ignored: "name"}.options()...)
					require.NoError(t, err)
					spec, err := update.NewSpec(req, secret...)
					require.NoError(t, err)

					var stored proto.Message = &descriptorpb.FileDescriptorProto{Package: new("old")}
					if create {
						stored = nil
					}

					writes := 0
					patch := sourcePatch(spec, stored, func(_ proto.Message, plan update.Plan) {
						writes++

						require.Nil(t, plan.Secret("name", req.Name), "the boundary ignores this secret")
					})
					patch.Create = func(_ context.Context, in proto.Message) (proto.Message, error) {
						writes++

						requireProtoEqual(t, want, in)

						return in, nil
					}
					out, err := patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask(),
						update.WithAllowMissing(create), update.WithValidateOnly(validate))
					require.NoError(t, err)

					requireProtoEqual(t, want, out)
					require.Nil(t, req.Name)

					if validate {
						require.Zero(t, writes)
					} else {
						require.Equal(t, 1, writes)
					}
				})
			}
		}
	}
}

func TestDeferredCreateIgnoresMaskAfterBoundarySanitization(t *testing.T) {
	for _, paths := range [][]string{nil, {}, {"*"}, {"name"}, {"package"}, {"missing"}, {"name", "bad..path"}} {
		for _, validate := range []bool{false, true} {
			t.Run(fmt.Sprintf("paths=%v/omitted=%t/validate=%t", paths, paths == nil, validate), func(t *testing.T) {
				req := &descriptorpb.FileDescriptorProto{
					Name: new("boundary-output"), Syntax: new("spec-output"), Package: new("new"), Dependency: []string{"retained"},
				}

				boundary := append(sourcePolicy{ignored: "name"}.options(), update.WithPathValidation(update.PathValidationDisabled))
				pending, err := update.Prepare(maskOf(paths), req, boundary...)
				require.NoError(t, err)
				spec, err := update.NewSpec(req, sourcePolicy{ignored: "syntax"}.options()...)
				require.NoError(t, err)

				want := &descriptorpb.FileDescriptorProto{Package: new("new"), Dependency: []string{"retained"}}
				writes := 0
				patch := sourcePatch(spec, nil, func(proto.Message, update.Plan) { t.Fatal("create reached Save") })
				patch.Create = func(_ context.Context, input proto.Message) (proto.Message, error) {
					writes++

					requireProtoEqual(t, want, input)

					return input, nil
				}

				proto.Reset(req) // Both consumers must use the captured R0, not this resource.

				out, err := patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", maskOf([]string{"also.invalid"}),
					update.WithAllowMissing(true), update.WithValidateOnly(validate))
				require.NoError(t, err)
				requireProtoEqual(t, want, out)

				if validate {
					require.Zero(t, writes)
				} else {
					require.Equal(t, 1, writes)
				}
			})
		}
	}
}

func TestDeferredRefusalLeavesRequestUnchanged(t *testing.T) {
	for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
		t.Run(entry, func(t *testing.T) {
			req := &descriptorpb.FileDescriptorProto{Name: new("ignored"), Package: new("changed")}
			stored := &descriptorpb.FileDescriptorProto{Package: new("old")}
			pending, err := update.Prepare(maskOf([]string{"name", "package"}), req,
				sourcePolicy{ignored: "name", immutable: []string{"package"}}.options()...)
			require.NoError(t, err)
			spec, err := update.NewSpec(req)
			require.NoError(t, err)
			ctx := update.ContextWithPlan(t.Context(), pending)
			req.Package = new("subsequent mutation")
			before := proto.Clone(req)

			if entry == "Spec" {
				_, err = spec.Plan(req, nil, update.WithPlanContext(ctx), update.WithCurrent(stored))
			} else {
				patch := sourcePatch(spec, stored, func(proto.Message, update.Plan) { t.Fatal("refusal reached Save") })
				_, err = patch.Run(ctx, req, "", nil, update.WithValidateOnly(entry == "ValidateOnly"))
			}

			requireProtectedRefusal(t, err, entry)
			requireProtoEqual(t, before, req)
			requireProtoEqual(t, &descriptorpb.FileDescriptorProto{Package: new("old")}, stored)
		})
	}
}

func TestDeferredBoundaryProtectedReplacementSource(t *testing.T) {
	boundary := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.FullName() {
		case "google.protobuf.DescriptorProto.options":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		case "google.protobuf.MessageOptions.deprecated":
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		default:
			return nil
		}
	}))
	specPolicy := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.FullName() == "google.protobuf.MessageOptions.deprecated" || fd.FullName() == "google.protobuf.DescriptorProto.options" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		}

		return nil
	}))

	for _, defaultSpec := range []bool{false, true} {
		for _, changed := range []bool{false, true} {
			for _, entry := range []string{"Complete", "Spec", "Patch", "ValidateOnly"} {
				t.Run(fmt.Sprintf("default-spec=%t/changed=%t/%s", defaultSpec, changed, entry), func(t *testing.T) {
					stored := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{{Name: new("old"),
						Options: &descriptorpb.MessageOptions{Deprecated: new(true), MapEntry: new(false)}}}}
					req := proto.CloneOf(stored)
					req.MessageType[0].Name = new("new")
					req.MessageType[0].Options.Deprecated = nil // ignored input inside protected replacement
					req.MessageType[0].Options.MapEntry = new(changed)
					want := proto.CloneOf(stored)

					want.MessageType[0].Name = new("new")
					if entry != "Complete" {
						want.MessageType[0].Options.Deprecated = nil
					}

					pending, err := update.Prepare(maskOf([]string{"message_type"}), req, boundary)
					require.NoError(t, err)

					opts := []update.Option{specPolicy}
					if defaultSpec {
						opts = nil
					}

					spec, err := update.NewSpec(req, opts...)
					require.NoError(t, err)

					var (
						paths []string
						got   proto.Message
					)

					switch entry {
					case "Complete":
						mask, completeErr := pending.Complete(stored)
						err, paths = completeErr, mask.ToPaths()
					case "Spec":
						plan, planErr := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithDeferredPlan(pending), update.WithCurrent(stored))
						err, paths = planErr, plan.Paths
					default:
						patch := sourcePatch(spec, stored, func(m proto.Message, _ update.Plan) {
							require.False(t, changed)
							requireProtoEqual(t, want, m)
						})
						got, err = patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask(),
							update.WithValidateOnly(entry == "ValidateOnly"))
					}

					if changed || (!defaultSpec && entry != "Complete") {
						requireProtectedRefusal(t, err, entry)
						return
					}

					require.NoError(t, err)

					if got == nil {
						got = proto.Clone(stored)
						require.NoError(t, update.MergePaths(got, req, paths))
					}

					requireProtoEqual(t, want, got)
				})
			}
		}
	}
}

func TestDeferredBoundaryUnselectedSecret(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, entry := range []string{"Spec", "Patch", "ValidateOnly"} {
			t.Run(fmt.Sprintf("changed=%t/%s", changed, entry), func(t *testing.T) {
				stored := &descriptorpb.FileDescriptorProto{Name: new("keep"), Package: new("old")}

				req := &descriptorpb.FileDescriptorProto{Name: new("keep"), Package: new("new")}
				if changed {
					req.Name = new("changed")
				}

				pending, err := update.Prepare(maskOf([]string{"package"}), req, update.WithImmutableFields("name"))
				require.NoError(t, err)
				spec, err := update.NewSpec(req, update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					if fd.Name() == "name" {
						return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
					}

					return nil
				})))
				require.NoError(t, err)
				ctx := update.ContextWithPlan(t.Context(), pending)

				var got proto.Message

				writes := 0

				if entry == "Spec" {
					var plan update.Plan

					plan, err = spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
					if err == nil {
						require.Equal(t, "keep", *plan.Secret("name", req.Name))

						got = proto.Clone(stored)
						require.NoError(t, update.MergePaths(got, req, plan.Paths))
					}
				} else {
					patch := sourcePatch(spec, stored, func(_ proto.Message, plan update.Plan) {
						writes++

						require.Equal(t, "keep", *plan.Secret("name", req.Name))
					})
					got, err = patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask(), update.WithValidateOnly(entry == "ValidateOnly"))
				}

				if changed {
					require.ErrorIs(t, err, update.ErrProtectedField)
					require.Zero(t, writes)

					var fieldErr *coreerrs.FieldError

					require.ErrorAs(t, err, &fieldErr)

					wantField := "updateMask" // the secret has no explicit mask index
					if entry != "Spec" {
						wantField = "resource.name"
					}

					require.Equal(t, wantField, fieldErr.Field)

					return
				}

				require.NoError(t, err)
				requireProtoEqual(t, &descriptorpb.FileDescriptorProto{Name: new("keep"), Package: new("new")}, got)

				if entry == "ValidateOnly" {
					require.Zero(t, writes)
				}
			})
		}
	}
}

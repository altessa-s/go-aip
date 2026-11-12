// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestMixedProtectedRestoration(t *testing.T) {
	for _, entry := range []string{"ApplyMask", "Spec", "Patch"} {
		t.Run(entry, func(t *testing.T) {
			req := dynamicpb.NewMessage((&descriptorpb.FileDescriptorProto{}).ProtoReflect().Descriptor())
			child := &descriptorpb.DescriptorProto{Name: new("old"), Options: &descriptorpb.MessageOptions{Deprecated: new(true)}}
			req.Mutable(req.Descriptor().Fields().ByName("message_type")).List().Append(protoreflect.ValueOfMessage(child.ProtoReflect()))
			stored := proto.Clone(req)
			child.Name = new("new")
			opts := []update.Option{update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
				if fd.FullName() == "google.protobuf.DescriptorProto.options" {
					return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
				}

				return nil
			}))}
			raw := &fieldmaskpb.FieldMask{Paths: []string{"message_type"}}
			spec, err := update.NewSpec(req, opts...)
			require.NoError(t, err)

			var paths []string

			switch entry {
			case "ApplyMask":
				mask, err := update.ApplyMask(raw, req, append(opts, update.WithCurrent(stored))...)
				require.NoError(t, err)

				paths = mask.ToPaths()
			case "Spec":
				plan, err := spec.Plan(req, raw, update.WithCurrent(stored))
				require.NoError(t, err)

				paths = plan.Paths
			case "Patch":
				patch := update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
					Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
					Read:         func(context.Context) (proto.Message, error) { return stored, nil }, ToProto: func(m proto.Message) proto.Message { return m },
					Save: func(_ context.Context, m proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
						return m, nil
					}}
				got, err := patch.Run(t.Context(), req, "", raw)
				require.NoError(t, err)
				requireProtoEqual(t, req, got)

				return
			}

			require.NoError(t, update.MergePaths(stored, req, paths))
			requireProtoEqual(t, req, stored)
		})
	}
}

// mixedMessage builds fresh nodes without cloning: a dynamic parent accepts
// either representation independently for each child. A generated parent has
// concrete Go fields and necessarily fixes its entire descendant representation.
func mixedMessage(t *testing.T, source protoreflect.Message, rng *rand.Rand, dynamicAllowed bool) protoreflect.Message {
	t.Helper()

	dynamic := dynamicAllowed && rng.IntN(2) == 0

	out := source.Type().New()
	if dynamic {
		out = dynamicpb.NewMessage(source.Descriptor())
	}

	fields := source.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !source.Has(fd) {
			continue
		}

		value := source.Get(fd)
		child := func(v protoreflect.Value, desc protoreflect.FieldDescriptor) protoreflect.Value {
			if desc.Message() != nil {
				return protoreflect.ValueOfMessage(mixedMessage(t, v.Message(), rng, dynamic))
			}

			if desc.Kind() == protoreflect.BytesKind {
				return protoreflect.ValueOfBytes(slices.Clone(v.Bytes()))
			}

			return v
		}

		switch {
		case fd.IsList():
			list := out.Mutable(fd).List()
			for j := range value.List().Len() {
				list.Append(child(value.List().Get(j), fd))
			}
		case fd.IsMap():
			target := out.Mutable(fd).Map()
			// Range order must not change the seeded representation choices.
			keys := []protoreflect.MapKey{}

			value.Map().Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool { keys = append(keys, k); return true })
			slices.SortFunc(keys, func(a, b protoreflect.MapKey) int { return strings.Compare(a.String(), b.String()) })

			for _, k := range keys {
				target.Set(k, child(value.Map().Get(k), fd.MapValue()))
			}
		default:
			out.Set(fd, child(value, fd))
		}
	}

	out.SetUnknown(slices.Clone(source.GetUnknown()))

	return out
}

func TestMixedRepresentationGenerator(t *testing.T) {
	entries := []string{"ApplyMask", "Apply", "ValidateStrict", "PrepareCurrent", "Complete", "Spec", "SpecDeferred", "Patch", "PatchDeferred",
		"StripUpdate", "StripUpdateCurrent", "StripCreate", "StripResponse", "Merge", "MergePaths"}

	for caseIndex, tc := range mixedCases() {
		for seed := range 4 {
			for _, entry := range entries {
				t.Run(fmt.Sprintf("%s/seed=%d/%s", tc.name, seed, entry), func(t *testing.T) {
					run := func(mixed bool) proto.Message {
						// Every pipeline gets independently constructed request and stored fixtures.
						factory := func(request bool) proto.Message {
							source := tc.makeMessage(request)

							offset := uint64(0)
							if request {
								offset = 1
							}

							rng := rand.New(rand.NewPCG(uint64(seed*2)+offset, uint64(caseIndex+203)))

							return mixedMessage(t, source.ProtoReflect(), rng, mixed).Interface()
						}

						return runMixedEntry(t, entry, tc, factory)
					}
					want := run(false)
					got := run(true)
					requireProtoEqual(t, want, got)
				})
			}
		}
	}
}

type mixedCase struct {
	name        string
	makeMessage func(bool) proto.Message
	paths       []string
	resolver    fieldbehavior.ResolverFunc
}

func mixedCases() []mixedCase {
	descriptor := func(request bool) proto.Message {
		name := "old"
		if request {
			name = "new"
		}

		opts := func() *descriptorpb.MessageOptions {
			return &descriptorpb.MessageOptions{Deprecated: new(true), MapEntry: new(true)}
		}

		return &descriptorpb.FileDescriptorProto{Name: new("file"), Options: &descriptorpb.FileOptions{JavaPackage: new("secret")},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: new(name), Options: opts(), NestedType: []*descriptorpb.DescriptorProto{{Name: new(name), Options: opts()}}},
				{Name: new(name), Options: opts()}}}
	}
	resolve := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.FullName() {
		case "google.protobuf.DescriptorProto.options":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		case "google.protobuf.MessageOptions.deprecated":
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		case "google.protobuf.FileDescriptorProto.options":
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}
		}

		return nil
	})
	resource := func(request bool) proto.Message {
		value := "old"
		if request {
			value = "new"
		}

		profile := func() *pb.Profile { return &pb.Profile{Id: "same", DisplayName: value, UpdatedAt: "server"} }

		return &pb.Resource{Name: "resource", Description: value, Profile: profile(), Aliases: []*pb.Profile{profile(), profile(), profile()},
			Labels: map[string]*pb.Profile{"a": profile(), "b": profile(), "c": profile()}}
	}

	var cases []mixedCase
	for i, paths := range [][]string{nil, {"*"}, {"message_type"}, {"message_type.*.name"}} {
		cases = append(cases, mixedCase{fmt.Sprintf("protected-message-%d", i), descriptor, paths, resolve})
	}

	for i, paths := range [][]string{nil, {"*"}, {"aliases", "labels"}, {"aliases.*.display_name", "labels.a"}} {
		cases = append(cases, mixedCase{fmt.Sprintf("collections-%d", i), resource, paths, nil})
	}

	for _, name := range []protoreflect.Name{"aliases", "labels", "profile"} {
		cases = append(cases, mixedCase{"protected-" + string(name), func(request bool) proto.Message {
			msg := resource(false).(*pb.Resource)
			if request {
				msg.Description = "new"
			}

			return msg
		}, []string{"*"}, func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == name {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		}})
	}

	for i, paths := range [][]string{nil, {"*"}, {"items"}} {
		cases = append(cases, mixedCase{fmt.Sprintf("dynamic-root-%d", i), func(request bool) proto.Message {
			value := "old"
			if request {
				value = "new"
			}

			return &structpb.Struct{Fields: map[string]*structpb.Value{
				"items": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{
					structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"value": structpb.NewStringValue(value)}}),
					structpb.NewStringValue(value)}}), "keep": structpb.NewBoolValue(true)}}
		}, paths, nil})
	}

	return cases
}

func runMixedEntry(t *testing.T, entry string, tc mixedCase, factory func(bool) proto.Message) proto.Message {
	t.Helper()

	req, stored := factory(true), factory(false)
	opts := []update.Option{update.WithIdentityFields(), update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
		return md.FullName() != "google.protobuf.DescriptorProto" && update.DefaultAtomicMessage(md)
	})}
	stripOpts := []fieldbehavior.Option{}

	if tc.resolver != nil {
		opts = append(opts, update.WithResolver(tc.resolver))
		stripOpts = append(stripOpts, fieldbehavior.WithResolver(tc.resolver))
	}

	raw := maskOf(tc.paths)

	mask := fieldmask.FromPaths(tc.paths...)
	if tc.paths == nil {
		mask = nil
	}

	known := append(slices.Clone(opts), update.WithCurrent(stored))

	var err error

	switch entry {
	case "ApplyMask":
		mask, err = update.ApplyMask(raw, req, known...)
	case "Apply":
		err = update.Apply(mask, req, known...)
	case "ValidateStrict":
		mask, err = update.ValidateStrict(mask, req, known...)
	case "PrepareCurrent":
		var pending *update.DeferredPlan

		pending, err = update.Prepare(raw, req, known...)
		if err == nil {
			mask = pending.Mask()
		}
	case "Complete":
		pending, prepareErr := update.Prepare(raw, req, opts...)
		require.NoError(t, prepareErr)

		mask, err = pending.Complete(stored)
		require.NoError(t, err)

		again, completeErr := pending.Complete(stored)
		require.NoError(t, completeErr)
		require.Equal(t, mask, again)
	case "StripCreate", "StripResponse":
		if entry == "StripCreate" {
			err = fieldbehavior.StripCreate(req, stripOpts...)
		} else {
			err = fieldbehavior.StripResponse(req, stripOpts...)
		}

		require.NoError(t, err)

		return req
	case "Merge", "MergePaths":
		paths := tc.paths
		if paths == nil {
			paths = []string{"*"}
		}

		if entry == "Merge" {
			err = update.Merge(fieldmask.FromPaths(paths...), stored, req, opts...)
		} else {
			if slices.Equal(paths, []string{"*"}) && req.ProtoReflect().Descriptor().FullName() != "google.protobuf.Struct" {
				paths = nil

				fields := req.ProtoReflect().Descriptor().Fields()
				for i := range fields.Len() {
					paths = append(paths, string(fields.Get(i).Name()))
				}
			}

			err = update.MergePaths(stored, req, paths, opts...)
		}

		require.NoError(t, err)

		return stored
	default:
		if strings.HasPrefix(entry, "StripUpdate") {
			if entry == "StripUpdateCurrent" {
				stripOpts = append(stripOpts, fieldbehavior.WithCurrent(stored))
			}

			require.NoError(t, fieldbehavior.StripUpdate(req, stripOpts...))

			raw = nil
		}

		spec, specErr := update.NewSpec(req, opts...)
		require.NoError(t, specErr)
		ctx := t.Context()

		if strings.HasSuffix(entry, "Deferred") {
			pending, prepareErr := update.Prepare(raw, req, opts...)
			require.NoError(t, prepareErr)

			ctx = update.ContextWithPlan(ctx, pending)
			raw = pending.Mask().ToProtoFieldMask()
		}

		if strings.HasPrefix(entry, "Patch") {
			var saved proto.Message

			patch := update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
				Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
				Read:         func(context.Context) (proto.Message, error) { return stored, nil }, ToProto: func(m proto.Message) proto.Message { return proto.Clone(m) },
				Save: func(_ context.Context, m proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
					saved = proto.Clone(m)

					return m, nil
				}}
			got, patchErr := patch.Run(ctx, req, "", raw)
			require.NoError(t, patchErr)
			requireProtoEqual(t, factory(false), stored)

			if saved != nil {
				requireProtoEqual(t, saved, got)
			}

			return got
		}

		plan, planErr := spec.Plan(req, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
		err = planErr
		mask = fieldmask.FromPaths(plan.Paths...)
	}

	require.NoError(t, err)
	requireProtoEqual(t, factory(false), stored)
	require.NoError(t, update.MergePaths(stored, req, mask.ToPaths(), opts...))

	return stored
}

func TestMixedGeneratorCoverage(t *testing.T) {
	seen := map[string]bool{}

	var visit func(protoreflect.Message)

	kind := func(msg protoreflect.Message) string {
		if _, ok := msg.Interface().(*dynamicpb.Message); ok {
			return "dynamic"
		}

		return "generated"
	}
	visit = func(msg protoreflect.Message) {
		msg.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			children := []protoreflect.Message{}

			switch {
			case fd.IsMap() && fd.MapValue().Message() != nil:
				v.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
					children = append(children, entry.Message())
					return true
				})
			case fd.IsList() && fd.Message() != nil:
				for i := range v.List().Len() {
					children = append(children, v.List().Get(i).Message())
				}
			case fd.Message() != nil && !fd.IsMap() && !fd.IsList():
				children = append(children, v.Message())
			}

			types := map[string]bool{}
			for _, child := range children {
				types[kind(child)] = true
				seen[kind(msg)+"/"+kind(child)] = true
				visit(child)
			}

			if len(types) == 2 {
				if fd.IsMap() {
					seen["mixed map"] = true
				}

				if fd.IsList() {
					seen["mixed list"] = true
				}
			}

			return true
		})
	}

	for i, tc := range mixedCases() {
		for seed := range 4 {
			request := mixedMessage(t, tc.makeMessage(true).ProtoReflect(), rand.New(rand.NewPCG(uint64(seed*2+1), uint64(i+203))), true)
			stored := mixedMessage(t, tc.makeMessage(false).ProtoReflect(), rand.New(rand.NewPCG(uint64(seed*2), uint64(i+203))), true)
			seen["roots "+kind(request)+"/"+kind(stored)] = true
			visit(request)
			visit(stored)
		}
	}

	for _, key := range []string{"roots dynamic/dynamic", "roots dynamic/generated", "roots generated/dynamic", "roots generated/generated",
		"dynamic/dynamic", "dynamic/generated", "generated/generated", "mixed map", "mixed list"} {
		require.True(t, seen[key], "seed corpus must exercise %s", key)
	}
	// Standard generated fields have concrete pointer types. The missing
	// generated/dynamic edge is impossible to construct through reflection.
	parent := (&descriptorpb.FileDescriptorProto{}).ProtoReflect()
	child := dynamicpb.NewMessage((&descriptorpb.DescriptorProto{}).ProtoReflect().Descriptor())

	require.Panics(t, func() {
		parent.Mutable(parent.Descriptor().Fields().ByName("message_type")).List().Append(protoreflect.ValueOfMessage(child))
	})
}

// Refusals must work across the same independently chosen representations as
// successful merges. The first two seeds force both generated/dynamic directions;
// the remaining seeds also vary the representations below dynamic parents.
func TestMixedRepresentationRefusalGenerator(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		for seed := range 6 {
			for _, entry := range []string{"ApplyMask", "Complete", "Spec", "SpecDeferred", "Patch", "PatchDeferred"} {
				t.Run(fmt.Sprintf("atomic=%t/seed=%d/%s", atomic, seed, entry), func(t *testing.T) {
					factory := func(request bool) proto.Message {
						value, offset := "old", uint64(0)
						if request {
							value, offset = "new", 1
						}

						source := &descriptorpb.FileDescriptorProto{Options: &descriptorpb.FileOptions{JavaPackage: new(value)}}

						if seed < 2 {
							if request == (seed == 0) {
								return source
							}

							out := dynamicpb.NewMessage(source.ProtoReflect().Descriptor())
							data, err := proto.Marshal(source)
							require.NoError(t, err)
							require.NoError(t, proto.Unmarshal(data, out))

							return out
						}

						rng := rand.New(rand.NewPCG(uint64(seed*2)+offset, 2702))

						return mixedMessage(t, source.ProtoReflect(), rng, true).Interface()
					}
					req, stored := factory(true), factory(false)
					beforeReq, beforeStored := proto.Clone(req), proto.Clone(stored)
					opts := []update.Option{update.WithAtomicMessage(nil), update.WithImmutableFields("options")}

					path, violationPath := "options.java_package", "options"
					if atomic {
						// A protected sub-field compared through an atomic replacement.
						path, violationPath = "options", "options.java_package"
						opts = []update.Option{update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
							return md.FullName() == "google.protobuf.FileOptions"
						}), update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
							if fd.FullName() == "google.protobuf.FileOptions.java_package" {
								return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
							}

							return nil
						}))}
					}

					raw := maskOf([]string{path})
					spec, err := update.NewSpec(req, opts...)
					require.NoError(t, err)
					ctx := t.Context()

					var pending *update.DeferredPlan

					if entry == "Complete" || strings.HasSuffix(entry, "Deferred") {
						pending, err = update.Prepare(raw, req, opts...)
						require.NoError(t, err)

						ctx = update.ContextWithPlan(ctx, pending)
						raw = pending.Mask().ToProtoFieldMask()
					}

					switch entry {
					case "ApplyMask":
						_, err = update.ApplyMask(raw, req, append(slices.Clone(opts), update.WithCurrent(stored))...)
					case "Complete":
						_, err = pending.Complete(stored)
					case "Spec", "SpecDeferred":
						_, err = spec.Plan(req, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
					default:
						patch := sourcePatch(spec, stored, func(proto.Message, update.Plan) { t.Fatal("refused update reached Save") })
						_, err = patch.Run(ctx, req, "", raw)
					}

					requireProtectedRefusal(t, err, entry)

					if entry == "ApplyMask" || entry == "Complete" {
						var behaviorErr *update.BehaviorError

						require.ErrorAs(t, err, &behaviorErr)
						require.Len(t, behaviorErr.Violations, 1)
						require.Equal(t, violationPath, behaviorErr.Violations[0].Path)
					} else {
						var fieldErr *coreerrs.FieldError

						require.ErrorAs(t, err, &fieldErr)

						wantField := "updateMask.paths[0]"
						if strings.HasPrefix(entry, "Patch") {
							wantField = "resource." + violationPath
						}

						require.Equal(t, wantField, fieldErr.Field)

						var aggregate *update.BehaviorError

						require.False(t, errors.As(err, &aggregate))
					}

					requireProtoEqual(t, beforeReq, req)
					requireProtoEqual(t, beforeStored, stored)
				})
			}
		}
	}
}

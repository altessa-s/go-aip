// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/typepb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/extract"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestMaskSpecPolicies(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	in := dynamicpb.NewMessage(md)
	in.Set(md.Fields().ByName("id"), protoreflect.ValueOfString("editable"))
	spec, err := update.NewSpec(in, update.WithIdentityFields("code"), update.WithUpdateMaskFieldName("changes"))
	require.NoError(t, err)
	plan, err := spec.Plan(in, maskOf([]string{"id"}))
	require.NoError(t, err)
	require.Equal(t, []string{"id"}, plan.Paths)

	_, err = spec.Plan(in, maskOf([]string{"code"}))
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, "changes.paths[0]", fe.Field)

	_, err = spec.Plan(in, maskOf([]string{"*", "id"}))
	require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone)
	fe, ok = errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, "changes.paths[0]", fe.Field, "the field of the offending \"*\"")

	atomic := update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Filters" })
	fd := md.Fields().ByName("filters")
	sub := in.Mutable(fd).Message()
	sub.Set(sub.Descriptor().Fields().ByName("keywords"), protoreflect.ValueOfString("x"))
	require.Contains(t, update.ImpliedPaths(in, atomic), "filters")
	require.Empty(t, update.ResolvePath(md, "filters.keywords", atomic))
	spec, err = update.NewSpec(in, atomic)
	require.NoError(t, err)
	_, err = spec.Plan(in, maskOf([]string{"filters.keywords"}))
	require.Error(t, err)

	dst := dynamicpb.NewMessage(md)
	require.NoError(t, update.MergePaths(dst, in, []string{"filters"}, atomic))
	require.Error(t, update.MergePaths(dst, in, []string{"filters.keywords"}, atomic))
}

func TestMaskSpecConstructorNil(t *testing.T) {
	t.Parallel()

	for _, m := range []proto.Message{nil, (*emptypb.Empty)(nil)} {
		_, err := update.NewSpec(m)
		require.ErrorIs(t, err, update.ErrMessageMismatch)
	}
}

// opaquePrecondition demonstrates a revision contract with no numeric ETag dependency.
type opaquePrecondition struct{}

func (opaquePrecondition) Parse(tag string) (string, error) { return tag, nil }
func (opaquePrecondition) Check(_ *emptypb.Empty, tag string) error {
	if tag != "revision-A" {
		return coreerrs.ErrConflict
	}

	return nil
}

func (opaquePrecondition) Missing(string) error { return coreerrs.ErrConflict }
func TestPatchOpaquePreconditionAndMismatch(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&emptypb.Empty{})
	require.NoError(t, err)

	p := update.Patch[*emptypb.Empty, proto.Message, string]{
		Spec: spec, Precondition: opaquePrecondition{},
		Read:    func(context.Context) (*emptypb.Empty, error) { return &emptypb.Empty{}, nil },
		ToProto: func(m *emptypb.Empty) proto.Message { return m },
		Save: func(context.Context, proto.Message, update.Plan, string) (*emptypb.Empty, error) {
			require.FailNow(t, "empty plan saved")
			return nil, nil
		},
	}
	_, err = p.Run(t.Context(), &emptypb.Empty{}, "revision-A", nil)
	require.NoError(t, err)
	_, err = p.Run(t.Context(), &emptypb.Empty{}, "stale", nil)
	require.ErrorIs(t, err, coreerrs.ErrConflict)

	p.ToProto = func(*emptypb.Empty) proto.Message { return &fieldmaskpb.FieldMask{} }
	_, err = p.Run(t.Context(), &emptypb.Empty{}, "revision-A", nil)
	require.ErrorIs(t, err, update.ErrMessageMismatch)
}

func TestDynamicMaskRoundTrip(t *testing.T) {
	t.Parallel()
	// Rebuild the descriptor, ensuring it differs from the generated singleton.
	maskFile, err := protodesc.NewFile(protodesc.ToFileDescriptorProto((&fieldmaskpb.FieldMask{}).ProtoReflect().Descriptor().ParentFile()), nil)
	require.NoError(t, err)

	registry := new(protoregistry.Files)
	require.NoError(t, registry.RegisterFile(maskFile))
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("dynamic_request.proto"), Package: new("dynamicrequest"), Syntax: new("proto3"), Dependency: []string{maskFile.Path()},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Request"), Field: []*descriptorpb.FieldDescriptorProto{{
			Name: new("changes"), Number: new(int32(1)), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.FieldMask"),
		}}}},
	}, registry)
	require.NoError(t, err)

	req := dynamicpb.NewMessage(file.Messages().Get(0))
	want := &fieldmaskpb.FieldMask{Paths: []string{"profile.name"}}
	require.NoError(t, extract.SetUpdateMask(req, want, extract.WithMaskField("changes")))
	got, ok := extract.ReadMask(req, extract.WithMaskField("changes"))
	require.True(t, ok)
	require.Equal(t, want.Paths, got.Paths)
	require.NoError(t, extract.SetUpdateMask(req, nil, extract.WithMaskField("changes")))
	_, ok = extract.ReadMask(req, extract.WithMaskField("changes"))
	require.False(t, ok)
}

func TestMaskSpecBehaviorResolver(t *testing.T) {
	t.Parallel()
	in := dynamicpb.NewMessage(planDescriptor(t))
	spec, err := update.NewSpec(in, update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "keywords" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		}

		if fd.Name() == "title" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	})))
	require.NoError(t, err)

	// The resolver marks filters.keywords OUTPUT_ONLY: naming it is ignored,
	// and replacing filters keeps its stored value.
	for path, want := range map[string][]string{"filters.keywords": nil, "filters": {"filters.region"}} {
		plan, err := spec.Plan(in, maskOf([]string{path}))
		require.NoError(t, err, path)
		require.Equal(t, want, plan.Paths, path)
	}
}

func TestUpdateEntryPointsBehaviorPolicy(t *testing.T) {
	t.Parallel()
	m := planResource(planDescriptor(t), map[string]string{"name": "set"})
	resolver := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "name" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	}))
	_, err := update.ValidateStrict(fieldmask.FieldMask{"name": nil}, m, resolver)
	require.Error(t, err)
	err = update.Apply((fieldmask.FieldMask{"name": nil}), m, resolver)
	require.Error(t, err)
	_, err = update.ValidateStrict(fieldmask.FieldMask{"name": nil}, nil, update.WithUpdateMaskFieldName("patch"))

	var strict *update.StrictError
	require.ErrorAs(t, err, &strict)
	require.Equal(t, "patch", strict.Violations[0].Path)
}

// TestStrictWildcardAtomicPolicy pins that "*" names an atomic message whole,
// compared with the stored resource: Status holds an IMMUTABLE origin, which
// a whole replacement cannot keep, so it is accepted when equal to the
// stored value and refused without the stored resource.
func TestStrictWildcardAtomicPolicy(t *testing.T) {
	t.Parallel()
	m := planResource(planDescriptor(t), nil)
	atomic := update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Status" })
	mask, err := update.ValidateStrict(fieldmask.FieldMask{"*": nil}, m, atomic, update.WithCurrent(proto.Clone(m)))
	require.NoError(t, err)
	require.Contains(t, mask, "status")
	require.Nil(t, mask["status"])

	_, err = update.ValidateStrict(fieldmask.FieldMask{"*": nil}, m, atomic)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

func TestAtomicUpdateRejectsPartialMutation(t *testing.T) {
	t.Parallel()
	m := planResource(planDescriptor(t), nil)
	atomic := update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Filters" })
	mask := fieldmask.FieldMask{"filters": fieldmask.FieldMask{"keywords": nil}}
	_, err := update.ValidateStrict(mask, m, atomic)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
	err = update.Apply(mask, m, atomic)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
}

func TestDropIdentifiersUsesUpdatePolicy(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	m := planResource(md, map[string]string{"name": "resource", "code": "new"})
	resolver := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "name" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}
		}

		return nil
	}))
	mask := fieldmask.FromPaths("code")
	require.NoError(t, update.Apply(mask, m, resolver))
	require.Contains(t, mask, "name")
	update.DropIdentifiers(mask, m, resolver)
	require.NotContains(t, mask, "name")
	require.Contains(t, mask, "code")
}

func TestPlanAtomicProtectedMessage(t *testing.T) {
	t.Parallel()
	m := planResource(planDescriptor(t), nil)
	atomic := update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Status" })
	spec, err := update.NewSpec(m, atomic)
	require.NoError(t, err)

	for _, path := range []string{"status", "*"} {
		_, err = spec.Plan(m, maskOf([]string{path}), update.WithCurrent(proto.Clone(m)))
		require.NoError(t, err, path)

		_, err = spec.Plan(m, maskOf([]string{path}))
		require.ErrorIs(t, err, update.ErrProtectedField, path)
	}
}

func TestNilAtomicPolicy(t *testing.T) {
	t.Parallel()

	msg := &typepb.Option{Value: &anypb.Any{TypeUrl: "example"}}
	policy := update.WithAtomicMessage(nil)
	spec, err := update.NewSpec(msg, policy)
	require.NoError(t, err)
	plan, err := spec.Plan(msg, maskOf([]string{"value.type_url"}))
	require.NoError(t, err)
	require.Equal(t, []string{"value.type_url"}, plan.Paths)
	require.Contains(t, update.ImpliedPaths(msg, policy), "value.type_url")
	require.Equal(t, "value.type_url", update.ResolvePath(msg.ProtoReflect().Descriptor(), "value.type_url", policy))

	dst := &typepb.Option{}
	require.NoError(t, update.MergePaths(dst, msg, plan.Paths, policy))
	require.Equal(t, "example", dst.GetValue().GetTypeUrl())
}

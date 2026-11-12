// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

var provenanceEntries = []string{"Spec", "SpecContext", "Patch", "ValidateOnly", "AllowMissing", "AllowMissingValidateOnly"}

// provenanceRefusal always uses the normalized mask at the handler. Expectations
// come from the fixture's raw mask, never from a second Spec call on K0.
func provenanceRefusal(t *testing.T, entry string, original, stored proto.Message, raw *fieldmaskpb.FieldMask,
	boundary, specOptions []update.Option,
) error {
	t.Helper()

	request := proto.Clone(original)
	before := proto.Clone(stored)
	pending, err := update.Prepare(raw, request, boundary...)
	require.NoError(t, err)
	spec, err := update.NewSpec(request, append(specOptions, update.WithUpdateMaskFieldName("request.mask"))...)
	require.NoError(t, err)
	ctx := update.ContextWithPlan(t.Context(), pending)

	switch entry {
	case "Spec":
		_, err = spec.Plan(request, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithDeferredPlan(pending))
	case "SpecContext":
		_, err = spec.Plan(request, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
	default:
		patch := sourcePatch(spec, stored, func(proto.Message, update.Plan) { t.Fatal("refused update reached Save") })
		patch.Create = func(context.Context, proto.Message) (proto.Message, error) {
			t.Fatal("existing resource reached Create")
			return nil, nil
		}
		_, err = patch.Run(ctx, request, "", pending.Mask().ToProtoFieldMask(),
			update.WithAllowMissing(strings.HasPrefix(entry, "AllowMissing")), update.WithValidateOnly(strings.Contains(entry, "ValidateOnly")))
	}

	requireProtoEqual(t, before, stored)

	return err
}

func assertClientMaskError(t *testing.T, err error, entry string, index int, path, field string, cause error) {
	t.Helper()

	require.ErrorIs(t, err, cause)

	var (
		mp *update.MaskPathError
		fe *coreerrs.FieldError
	)

	require.ErrorAs(t, err, &mp)
	require.Equal(t, index, mp.Index)
	require.Equal(t, path, mp.Path)
	require.Equal(t, field, mp.Field)
	require.ErrorAs(t, err, &fe)

	want := "request.mask"
	if index >= 0 {
		want = fmt.Sprintf("request.mask.paths[%d]", index)
	}

	if errors.Is(err, update.ErrProtectedField) && !strings.HasPrefix(entry, "Spec") {
		want = "resource." + field
	}

	require.Equal(t, want, fe.Field)

	_, aggregate := errors.AsType[*update.BehaviorError](err)
	_, strict := errors.AsType[*update.StrictError](err)

	require.False(t, aggregate)
	require.False(t, strict)
}

func TestDeferredClientMaskProvenance(t *testing.T) {
	stored := &descriptorpb.FileDescriptorProto{Package: new("old")}
	request := &descriptorpb.FileDescriptorProto{Name: new("ignored")}
	boundary := sourcePolicy{ignored: "name"}.options()
	specOptions := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"google.protobuf.FileDescriptorProto.package": annotations.FieldBehavior_REQUIRED,
	}}.options()

	for _, entry := range provenanceEntries {
		t.Run(entry, func(t *testing.T) {
			err := provenanceRefusal(t, entry, request, stored, maskOf([]string{"name", "package"}), boundary, specOptions)
			assertClientMaskError(t, err, entry, 1, "package", "package", update.ErrBehaviorViolation)
		})
	}
}

func TestDeferredClientMaskProvenanceGenerator(t *testing.T) {
	rng := rand.New(rand.NewPCG(203, 30))
	boundary := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"google.protobuf.FileDescriptorProto.name":            annotations.FieldBehavior_OUTPUT_ONLY,
		"google.protobuf.FileDescriptorProto.syntax":          annotations.FieldBehavior_OUTPUT_ONLY,
		"google.protobuf.FileDescriptorProto.weak_dependency": annotations.FieldBehavior_OUTPUT_ONLY,
	}}.options()
	boundary = append(boundary, update.WithPathValidation(update.PathValidationDisabled))
	ignored := []string{"name", "syntax", "weakDependency", "weak_dependency"}

	for sample := range 8 {
		for _, kind := range []string{"required", "unknown", "protected"} {
			request := &descriptorpb.FileDescriptorProto{Name: new("ignored"), Syntax: new("ignored"), Package: new("new")}
			stored := &descriptorpb.FileDescriptorProto{PublicDependency: []int32{1}, Package: new("old")}

			path, field, cause := "publicDependency", "public_dependency", update.ErrBehaviorViolation
			if sample%2 == 0 {
				path = "public_dependency"
			}

			policy := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
				"google.protobuf.FileDescriptorProto.public_dependency": annotations.FieldBehavior_REQUIRED,
			}}

			switch kind {
			case "unknown":
				path, field, cause = "unknownField", "", fieldmask.ErrInvalidPath
			case "protected":
				policy.fields["google.protobuf.FileDescriptorProto.public_dependency"] = annotations.FieldBehavior_IMMUTABLE
				cause = update.ErrProtectedField
			}

			paths := make([]string, 3+rng.IntN(10))
			for i := range paths {
				paths[i] = ignored[rng.IntN(len(ignored))]
			}

			// Record the failing index while building the raw request. Include a
			// surviving mutable entry as well as ignored entries on either side.
			paths[rng.IntN(len(paths))] = "package"
			index := rng.IntN(len(paths))
			paths[index] = path

			for _, entry := range provenanceEntries {
				t.Run(fmt.Sprintf("%d/%s/%s", sample, kind, entry), func(t *testing.T) {
					err := provenanceRefusal(t, entry, request, stored, maskOf(paths), boundary, policy.options())
					assertClientMaskError(t, err, entry, index, path, field, cause)
				})
			}
		}
	}
}

func TestDeferredRawMaskLimits(t *testing.T) {
	request := &descriptorpb.FileDescriptorProto{Name: new("ignored"), Package: new("new")}
	stored := &descriptorpb.FileDescriptorProto{Package: new("old")}

	for _, paths := range [][]string{{"name", "package"}, {"name", "name"}} {
		for _, entry := range provenanceEntries {
			t.Run(fmt.Sprintf("%v/%s", paths, entry), func(t *testing.T) {
				err := provenanceRefusal(t, entry, request, stored, maskOf(paths), sourcePolicy{ignored: "name"}.options(),
					[]update.Option{update.WithMaxPaths(1)})
				assertClientMaskError(t, err, entry, -1, "", "", fieldmask.ErrMaxPathsExceeded)
				require.Contains(t, err.Error(), "2 paths")
			})
		}
	}
}

func TestDeferredPinnedWildcardProvenance(t *testing.T) {
	md := collectionDescriptor(t)
	stored := collectionRoot(md, map[string]map[string]string{
		"k": {"note": "keep"}, "a": {"note": "old"}, "b": {"note": "old"}, "c": {"note": "old"},
	}, nil, nil)
	request := collectionRoot(md, map[string]map[string]string{
		"k": {"free": "ignored"}, "a": {}, "b": {}, "c": {},
	}, nil, nil)
	// Two client paths become three concrete paths; backticks must also survive.
	raw := maskOf([]string{"items.k.free", "`items`.*.`note`"})
	boundary := sourcePolicy{ignored: "free"}.options()

	for _, entry := range provenanceEntries {
		t.Run(entry, func(t *testing.T) {
			policy := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
				"colltest.Elem.note": annotations.FieldBehavior_REQUIRED,
			}}
			err := provenanceRefusal(t, entry, request, stored, raw, boundary, policy.options())
			assertClientMaskError(t, err, entry, 1, "`items`.*.`note`", "items.a.note", update.ErrBehaviorViolation)
		})
		t.Run(entry+"/expansion-limit", func(t *testing.T) {
			err := provenanceRefusal(t, entry, request, stored, raw, boundary,
				append(sourcePolicy{}.options(), update.WithMaxPaths(2)))
			assertClientMaskError(t, err, entry, 1, "`items`.*.`note`", "", fieldmask.ErrMaxPathsExceeded)
		})
	}
}

func TestDeferredFinalGuardClientAttribution(t *testing.T) {
	stored := &descriptorpb.FileDescriptorProto{Package: new("old"), Syntax: new("old")}
	request := &descriptorpb.FileDescriptorProto{Name: new("ignored"), Package: new("new"), Syntax: new("new")}

	for _, entry := range provenanceEntries {
		t.Run(entry, func(t *testing.T) {
			err := provenanceRefusal(t, entry, request, stored, maskOf([]string{"name", "syntax"}),
				sourcePolicy{ignored: "name", immutable: []string{"package"}}.options(), sourcePolicy{secret: "package"}.options())
			// The secret is outside the mask; the final guard must keep its
			// resource attribution and the bare configured mask field.
			assertClientMaskError(t, err, entry, -1, "", "package", update.ErrProtectedField)
		})
	}
}

func TestOneStageClientMaskProvenance(t *testing.T) {
	request := &descriptorpb.FileDescriptorProto{Name: new("ignored")}
	stored := &descriptorpb.FileDescriptorProto{PublicDependency: []int32{1}}
	raw := maskOf([]string{"name", "publicDependency"})
	policy := sourcePolicy{ignored: "name", fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"google.protobuf.FileDescriptorProto.public_dependency": annotations.FieldBehavior_REQUIRED,
	}}

	for _, entry := range []string{"Prepare", "ApplyMask", "Spec"} {
		t.Run(entry, func(t *testing.T) {
			opts := policy.options()

			var err error

			switch entry {
			case "Prepare":
				_, err = update.Prepare(raw, proto.Clone(request), opts...)
			case "ApplyMask":
				_, err = update.ApplyMask(raw, proto.Clone(request), append(opts, update.WithCurrent(stored))...)
			case "Spec":
				spec, specErr := update.NewSpec(request, append(opts, update.WithUpdateMaskFieldName("request.mask"))...)
				require.NoError(t, specErr)

				_, err = spec.Plan(proto.Clone(request), raw, update.WithCurrent(stored))
			}

			if entry == "Spec" {
				assertClientMaskError(t, err, entry, 1, "publicDependency", "public_dependency", update.ErrBehaviorViolation)
				return
			}

			var be *update.BehaviorError
			require.ErrorAs(t, err, &be)
			require.Len(t, be.Violations, 1)
			require.Equal(t, "public_dependency", be.Violations[0].Path)

			_, indexed := errors.AsType[*update.MaskPathError](err)
			require.False(t, indexed, "base APIs retain resource-path errors")
		})
	}
}

func TestDeferredAllowMissingIgnoresRawMaskLimits(t *testing.T) {
	for _, validate := range []bool{false, true} {
		t.Run(fmt.Sprint(validate), func(t *testing.T) {
			original := &descriptorpb.FileDescriptorProto{Name: new("original"), Package: new("new")}
			// allow_missing ignores mask limits, but the boundary's OUTPUT_ONLY
			// clearing still applies to the input used for creation.
			want := &descriptorpb.FileDescriptorProto{Package: new("new")}
			request := proto.CloneOf(original)
			pending, err := update.Prepare(maskOf([]string{"name", "package"}), request, sourcePolicy{ignored: "name"}.options()...)
			require.NoError(t, err)
			spec, err := update.NewSpec(request, update.WithMaxPaths(1))
			require.NoError(t, err)

			writes := 0
			patch := sourcePatch(spec, nil, func(proto.Message, update.Plan) { t.Fatal("missing resource reached Save") })
			patch.Create = func(_ context.Context, in proto.Message) (proto.Message, error) {
				writes++

				requireProtoEqual(t, want, in)

				return in, nil
			}
			ctx := update.ContextWithPlan(t.Context(), pending)
			got, err := patch.Run(ctx, request, "", pending.Mask().ToProtoFieldMask(), update.WithAllowMissing(true), update.WithValidateOnly(validate))
			require.NoError(t, err)
			requireProtoEqual(t, want, got)

			if validate {
				require.Zero(t, writes)
			} else {
				require.Equal(t, 1, writes)
			}
		})
	}
}

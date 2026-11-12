// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestDeferredOriginalMaskMapping(t *testing.T) {
	mapper := newMapper(t)

	for _, entry := range []string{"Spec", "Patch", "ValidateOnly", "AllowMissing"} {
		for _, spelling := range []string{"package", "`package`"} {
			t.Run(entry+"/"+spelling, func(t *testing.T) {
				request := &descriptorpb.FileDescriptorProto{Name: new("ignored")}
				stored := &descriptorpb.FileDescriptorProto{Package: new("old")}
				raw := &fieldmaskpb.FieldMask{Paths: []string{"name", spelling}}
				boundary := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					if fd.Name() == "name" {
						return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
					}

					return nil
				})
				specPolicy := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					if fd.Name() == "package" {
						return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
					}

					return nil
				})
				pending, err := update.Prepare(raw, request, update.WithResolver(boundary))
				require.NoError(t, err)
				spec, err := update.NewSpec(request, update.WithResolver(specPolicy), update.WithUpdateMaskFieldName("request.mask"))
				require.NoError(t, err)

				ctx := update.ContextWithPlan(t.Context(), pending)
				if entry == "Spec" {
					_, err = spec.Plan(request, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
				} else {
					patch := update.Patch[*descriptorpb.FileDescriptorProto, *descriptorpb.FileDescriptorProto, *int64]{
						Field: "resource", Spec: spec,
						Precondition: update.Int64Precondition[*descriptorpb.FileDescriptorProto]{Version: func(*descriptorpb.FileDescriptorProto) int64 { return 1 }},
						Read:         func(context.Context) (*descriptorpb.FileDescriptorProto, error) { return stored, nil },
						ToProto:      func(m *descriptorpb.FileDescriptorProto) *descriptorpb.FileDescriptorProto { return proto.CloneOf(m) },
						Save: func(context.Context, *descriptorpb.FileDescriptorProto, update.Plan, *int64) (*descriptorpb.FileDescriptorProto, error) {
							t.Fatal("refused request reached Save")
							return nil, nil
						},
						Create: func(context.Context, *descriptorpb.FileDescriptorProto) (*descriptorpb.FileDescriptorProto, error) {
							t.Fatal("existing resource reached Create")
							return nil, nil
						},
					}
					_, err = patch.Run(ctx, request, "", pending.Mask().ToProtoFieldMask(),
						update.WithValidateOnly(entry == "ValidateOnly"), update.WithAllowMissing(entry == "AllowMissing"))
				}

				require.ErrorIs(t, err, update.ErrBehaviorViolation)
				mp, ok := errors.AsType[*update.MaskPathError](err)
				require.True(t, ok)
				require.Equal(t, 1, mp.Index)
				require.Equal(t, spelling, mp.Path)
				require.Equal(t, "package", mp.Field)

				fe, ok := errors.AsType[*coreerrs.FieldError](err)
				require.True(t, ok)
				require.Equal(t, "request.mask.paths[1]", fe.Field)

				mapped := mapper.Map(err)
				require.Equal(t, code.Code_INVALID_ARGUMENT, mapped.Code())
				require.Equal(t, []string{"request.mask.paths[1]"}, refusalFields(mapped))
			})
		}
	}
}

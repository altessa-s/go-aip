// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/extract"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestExtractUpdateMask(t *testing.T) {
	t.Parallel()

	t.Run("returns mask and resource", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n", TenantId: "t"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
		}

		mask, res, ok := extract.UpdateMask(req)
		require.True(t, ok)
		require.Equal(t, []string{"name", "tenant_id"}, mask.GetPaths())

		r, isResource := res.(*pb.Resource)
		require.True(t, isResource)
		require.Equal(t, "n", r.GetName())
	})

	t.Run("missing mask returns false", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource: &pb.Resource{Name: "n"},
		}

		_, _, ok := extract.UpdateMask(req)
		require.False(t, ok)
	})

	t.Run("missing resource returns false", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}

		_, _, ok := extract.UpdateMask(req)
		require.False(t, ok)
	})

	t.Run("nil request returns false", func(t *testing.T) {
		t.Parallel()

		_, _, ok := extract.UpdateMask(nil)
		require.False(t, ok)
	})

	t.Run("wrong message shape returns false", func(t *testing.T) {
		t.Parallel()

		_, _, ok := extract.UpdateMask(&pb.Resource{Name: "n"})
		require.False(t, ok)
	})

	t.Run("override mask field name", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		_, _, ok := extract.UpdateMask(req, extract.WithMaskField("does_not_exist"))
		require.False(t, ok)
	})

	t.Run("override resource field name", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		_, res, ok := extract.UpdateMask(req, extract.WithResourceField("resource"))
		require.True(t, ok)

		_, isResource := res.(*pb.Resource)
		require.True(t, isResource)
	})

	t.Run("override missing resource field returns false", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		_, _, ok := extract.UpdateMask(req, extract.WithResourceField("does_not_exist"))
		require.False(t, ok)
	})
}

func TestExtractReadMask(t *testing.T) {
	t.Parallel()

	t.Run("returns mask", func(t *testing.T) {
		t.Parallel()

		req := &pb.GetResourceRequest{
			Name:     "id",
			ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
		}

		mask, ok := extract.ReadMask(req)
		require.True(t, ok)
		require.Equal(t, []string{"name", "tenant_id"}, mask.GetPaths())
	})

	t.Run("missing mask returns false", func(t *testing.T) {
		t.Parallel()

		req := &pb.GetResourceRequest{Name: "id"}
		_, ok := extract.ReadMask(req)
		require.False(t, ok)
	})

	t.Run("nil request returns false", func(t *testing.T) {
		t.Parallel()

		_, ok := extract.ReadMask(nil)
		require.False(t, ok)
	})

	t.Run("wrong message shape returns false", func(t *testing.T) {
		t.Parallel()

		_, ok := extract.ReadMask(&pb.Resource{Name: "n"})
		require.False(t, ok)
	})
}

func TestSetUpdateMask(t *testing.T) {
	t.Parallel()

	t.Run("writes mask back to request", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "create_time"}},
		}

		cleaned := &fieldmaskpb.FieldMask{Paths: []string{"name"}}
		require.NoError(t, extract.SetUpdateMask(req, cleaned))
		require.Equal(t, []string{"name"}, req.GetUpdateMask().GetPaths())
	})

	t.Run("nil mask clears the field", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		require.NoError(t, extract.SetUpdateMask(req, nil))
		require.False(t, req.ProtoReflect().Has(
			req.ProtoReflect().Descriptor().Fields().ByName("update_mask"),
		))
	})

	t.Run("nil request returns ErrFieldNotSettable", func(t *testing.T) {
		t.Parallel()

		err := extract.SetUpdateMask(nil, &fieldmaskpb.FieldMask{})
		require.ErrorIs(t, err, extract.ErrFieldNotSettable)
	})

	t.Run("wrong message shape returns ErrFieldNotSettable", func(t *testing.T) {
		t.Parallel()

		err := extract.SetUpdateMask(&pb.Resource{}, &fieldmaskpb.FieldMask{})
		require.ErrorIs(t, err, extract.ErrFieldNotSettable)
	})

	t.Run("custom mask field name", func(t *testing.T) {
		t.Parallel()

		err := extract.SetUpdateMask(
			&pb.UpdateResourceRequest{}, &fieldmaskpb.FieldMask{},
			extract.WithMaskField("does_not_exist"),
		)
		require.ErrorIs(t, err, extract.ErrFieldNotSettable)
	})
}

func TestExtractUpdateMaskReturnsProtoMessage(t *testing.T) {
	t.Parallel()

	req := &pb.UpdateResourceRequest{
		Resource:   &pb.Resource{},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}

	_, res, ok := extract.UpdateMask(req)
	require.True(t, ok)

	var _ proto.Message = res
}

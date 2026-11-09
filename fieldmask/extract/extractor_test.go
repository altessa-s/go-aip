// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/extract"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestNewUpdateExtractor(t *testing.T) {
	t.Parallel()

	t.Run("nested mask via typed getters", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask {
				return r.GetOptions().GetUpdateMask()
			},
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			func(r *pb.NestedUpdateResourceRequest, m *fieldmaskpb.FieldMask) {
				if r.Options == nil {
					r.Options = &pb.NestedUpdateResourceRequest_Options{}
				}

				r.Options.UpdateMask = m
			},
		)

		req := &pb.NestedUpdateResourceRequest{
			Resource: &pb.Resource{Name: "n"},
			Options: &pb.NestedUpdateResourceRequest_Options{
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			},
		}

		mask, res, writeback, ok := extractor(t.Context(), req)
		require.True(t, ok)
		require.Equal(t, []string{"name"}, mask.GetPaths())
		require.NotNil(t, res)
		require.NotNil(t, writeback)

		writeback(&fieldmaskpb.FieldMask{Paths: []string{"name", "description"}})
		require.Equal(t, []string{"name", "description"}, req.GetOptions().GetUpdateMask().GetPaths())
	})

	t.Run("type mismatch returns ok=false", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			nil,
		)

		_, _, _, ok := extractor(t.Context(), &pb.Resource{})
		require.False(t, ok)
	})

	t.Run("nil mask returns ok=false", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			nil,
		)

		req := &pb.NestedUpdateResourceRequest{Resource: &pb.Resource{Name: "n"}}
		_, _, _, ok := extractor(t.Context(), req)
		require.False(t, ok)
	})

	t.Run("typed-nil resource returns ok=false", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
			func(*pb.NestedUpdateResourceRequest) *pb.Resource { return nil },
			nil,
		)

		req := &pb.NestedUpdateResourceRequest{
			Options: &pb.NestedUpdateResourceRequest_Options{
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			},
		}
		_, _, _, ok := extractor(t.Context(), req)
		require.False(t, ok)
	})

	t.Run("nil setMask skips writeback", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			nil,
		)

		req := &pb.NestedUpdateResourceRequest{
			Resource: &pb.Resource{Name: "n"},
			Options: &pb.NestedUpdateResourceRequest_Options{
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			},
		}
		_, _, writeback, ok := extractor(t.Context(), req)
		require.True(t, ok)
		require.Nil(t, writeback)
	})

	t.Run("nil getter returns ok=false", func(t *testing.T) {
		t.Parallel()

		var getMask func(*pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask

		extractor := extract.NewUpdateFunc(
			getMask,
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			nil,
		)

		_, _, _, ok := extractor(t.Context(), &pb.NestedUpdateResourceRequest{})
		require.False(t, ok)
	})
}

func TestIsCanonicalUpdateRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		req  proto.Message
		want bool
	}{
		{name: "canonical", req: &pb.UpdateResourceRequest{}, want: true},
		{name: "nested mask requires custom extractor", req: &pb.NestedUpdateResourceRequest{}, want: false},
		{name: "no mask field", req: &pb.Resource{}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, extract.IsCanonicalUpdateRequest(tc.req))
		})
	}
}

func TestNewReadExtractor(t *testing.T) {
	t.Parallel()

	t.Run("nested read mask", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewReadFunc(
			func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask {
				return r.GetOptions().GetReadMask()
			},
		)

		req := &pb.NestedGetResourceRequest{
			Name: "id-1",
			Options: &pb.NestedGetResourceRequest_Options{
				ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "description"}},
			},
		}

		mask, ok := extractor(t.Context(), req)
		require.True(t, ok)
		require.Equal(t, []string{"name", "description"}, mask.GetPaths())
	})

	t.Run("type mismatch returns ok=false", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewReadFunc(
			func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetReadMask() },
		)

		_, ok := extractor(t.Context(), &pb.Resource{})
		require.False(t, ok)
	})

	t.Run("absent mask returns ok=false", func(t *testing.T) {
		t.Parallel()

		extractor := extract.NewReadFunc(
			func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetReadMask() },
		)

		_, ok := extractor(t.Context(), &pb.NestedGetResourceRequest{Name: "x"})
		require.False(t, ok)
	})
}

func TestDefaultUpdateExtractor(t *testing.T) {
	t.Parallel()

	extractor := extract.DefaultUpdate()

	t.Run("flat AIP-134 shape works", func(t *testing.T) {
		t.Parallel()

		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		mask, res, writeback, ok := extractor(t.Context(), req)
		require.True(t, ok)
		require.Equal(t, []string{"name"}, mask.GetPaths())
		require.NotNil(t, res)
		require.NotNil(t, writeback)

		writeback(&fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}})
		require.Equal(t, []string{"name", "tenant_id"}, req.GetUpdateMask().GetPaths())
	})

	t.Run("nested shape returns ok=false", func(t *testing.T) {
		t.Parallel()

		req := &pb.NestedUpdateResourceRequest{
			Resource: &pb.Resource{Name: "n"},
			Options: &pb.NestedUpdateResourceRequest_Options{
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			},
		}
		_, _, _, ok := extractor(t.Context(), req)
		require.False(t, ok, "default extractor only knows top-level update_mask")
	})
}

func TestDefaultReadExtractor(t *testing.T) {
	t.Parallel()

	extractor := extract.DefaultRead()

	req := &pb.GetResourceRequest{
		Name:     "id-1",
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}
	mask, ok := extractor(t.Context(), req)
	require.True(t, ok)
	require.Equal(t, []string{"name"}, mask.GetPaths())
}

func TestChainReadExtractors(t *testing.T) {
	t.Parallel()

	mask := &fieldmaskpb.FieldMask{Paths: []string{"name"}}
	matching := func(_ context.Context, _ proto.Message) (*fieldmaskpb.FieldMask, bool) {
		return mask, true
	}
	nonMatching := func(_ context.Context, _ proto.Message) (*fieldmaskpb.FieldMask, bool) {
		return nil, false
	}

	t.Run("first ok wins", func(t *testing.T) {
		t.Parallel()

		second := &fieldmaskpb.FieldMask{Paths: []string{"description"}}
		chain := extract.ChainRead(
			matching,
			func(_ context.Context, _ proto.Message) (*fieldmaskpb.FieldMask, bool) { return second, true },
		)

		got, ok := chain(t.Context(), &pb.GetResourceRequest{})
		require.True(t, ok)
		require.Same(t, mask, got)
	})

	t.Run("first miss falls through to next", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainRead(nonMatching, matching)

		got, ok := chain(t.Context(), &pb.GetResourceRequest{})
		require.True(t, ok)
		require.Same(t, mask, got)
	})

	t.Run("all miss returns ok=false", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainRead(nonMatching, nonMatching)
		_, ok := chain(t.Context(), &pb.GetResourceRequest{})
		require.False(t, ok)
	})

	t.Run("nil entries skipped", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainRead(nil, matching, nil)
		got, ok := chain(t.Context(), &pb.GetResourceRequest{})
		require.True(t, ok)
		require.Same(t, mask, got)
	})

	t.Run("zero functions returns no-op", func(t *testing.T) {
		t.Parallel()

		for _, chain := range []extract.ReadFunc{
			extract.ChainRead(),
			extract.ChainRead(nil, nil),
		} {
			require.NotNil(t, chain)
			mask, ok := chain(t.Context(), &pb.GetResourceRequest{})
			require.False(t, ok)
			require.Nil(t, mask)
		}
	})
}

func TestChainUpdateExtractors(t *testing.T) {
	t.Parallel()

	mask := &fieldmaskpb.FieldMask{Paths: []string{"name"}}
	res := &pb.Resource{Name: "n"}
	wb := func(*fieldmaskpb.FieldMask) {}

	matching := func(_ context.Context, _ proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
		return mask, res, wb, true
	}
	nonMatching := func(_ context.Context, _ proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
		return nil, nil, nil, false
	}

	t.Run("first ok wins", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainUpdate(matching, nonMatching)
		m, r, w, ok := chain(t.Context(), &pb.UpdateResourceRequest{})
		require.True(t, ok)
		require.Same(t, mask, m)
		require.Same(t, res, r)
		require.NotNil(t, w)
	})

	t.Run("first miss falls through", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainUpdate(nonMatching, matching)
		_, _, _, ok := chain(t.Context(), &pb.UpdateResourceRequest{})
		require.True(t, ok)
	})

	t.Run("all miss returns ok=false", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainUpdate(nonMatching, nonMatching)
		_, _, _, ok := chain(t.Context(), &pb.UpdateResourceRequest{})
		require.False(t, ok)
	})

	t.Run("nil entries skipped", func(t *testing.T) {
		t.Parallel()

		chain := extract.ChainUpdate(nil, matching)
		_, _, _, ok := chain(t.Context(), &pb.UpdateResourceRequest{})
		require.True(t, ok)
	})

	t.Run("zero functions returns no-op", func(t *testing.T) {
		t.Parallel()

		for _, chain := range []extract.UpdateFunc{
			extract.ChainUpdate(),
			extract.ChainUpdate(nil, nil),
		} {
			require.NotNil(t, chain)
			mask, res, wb, ok := chain(t.Context(), &pb.UpdateResourceRequest{})
			require.False(t, ok)
			require.Nil(t, mask)
			require.Nil(t, res)
			require.Nil(t, wb)
		}
	})
}

func TestNewUpdateExtractor_ReturnsProtoMessage(t *testing.T) {
	t.Parallel()

	extractor := extract.NewUpdateFunc(
		func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
		func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
		nil,
	)

	req := &pb.NestedUpdateResourceRequest{
		Resource: &pb.Resource{},
		Options: &pb.NestedUpdateResourceRequest_Options{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		},
	}

	_, res, _, ok := extractor(t.Context(), req)
	require.True(t, ok)

	var _ proto.Message = res
}

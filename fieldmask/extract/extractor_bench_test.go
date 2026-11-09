// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/extract"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkNewUpdateExtractor(b *testing.B) {
	extractor := extract.NewUpdateFunc(
		func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
		func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
		func(r *pb.NestedUpdateResourceRequest, m *fieldmaskpb.FieldMask) {
			r.Options.UpdateMask = m
		},
	)
	req := &pb.NestedUpdateResourceRequest{
		Resource: &pb.Resource{Name: "n"},
		Options: &pb.NestedUpdateResourceRequest_Options{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "description"}},
		},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _, _, _ = extractor(b.Context(), req)
	}
}

func BenchmarkNewReadExtractor(b *testing.B) {
	extractor := extract.NewReadFunc(
		func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetReadMask() },
	)
	req := &pb.NestedGetResourceRequest{
		Name: "id",
		Options: &pb.NestedGetResourceRequest_Options{
			ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = extractor(b.Context(), req)
	}
}

func BenchmarkDefaultUpdateExtractor(b *testing.B) {
	extractor := extract.DefaultUpdate()
	req := &pb.UpdateResourceRequest{
		Resource:   &pb.Resource{Name: "n", TenantId: "t"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _, _, _ = extractor(b.Context(), req)
	}
}

func BenchmarkChainReadExtractors_FirstHit(b *testing.B) {
	inner := extract.DefaultRead()
	chain := extract.ChainRead(inner, inner)
	req := &pb.GetResourceRequest{
		Name:     "id",
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = chain(b.Context(), req)
	}
}

func BenchmarkChainReadExtractors_SecondHit(b *testing.B) {
	miss := func(context.Context, proto.Message) (*fieldmaskpb.FieldMask, bool) { return nil, false }
	hit := extract.DefaultRead()
	chain := extract.ChainRead(miss, hit)
	req := &pb.GetResourceRequest{
		Name:     "id",
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = chain(b.Context(), req)
	}
}

func BenchmarkDefaultReadExtractor(b *testing.B) {
	extractor := extract.DefaultRead()
	req := &pb.GetResourceRequest{
		Name:     "id",
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = extractor(b.Context(), req)
	}
}

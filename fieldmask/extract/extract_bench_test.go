// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/extract"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkExtractUpdateMask(b *testing.B) {
	req := &pb.UpdateResourceRequest{
		Resource:   &pb.Resource{Name: "n", TenantId: "t"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _, _ = extract.UpdateMask(req)
	}
}

func BenchmarkExtractReadMask(b *testing.B) {
	req := &pb.GetResourceRequest{
		Name:     "id",
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tenant_id"}},
	}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = extract.ReadMask(req)
	}
}

func BenchmarkSetUpdateMask(b *testing.B) {
	cleaned := &fieldmaskpb.FieldMask{Paths: []string{"name"}}

	b.ReportAllocs()

	for b.Loop() {
		req := &pb.UpdateResourceRequest{
			Resource:   &pb.Resource{Name: "n"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "create_time"}},
		}
		_ = extract.SetUpdateMask(req, cleaned)
	}
}

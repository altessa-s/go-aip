// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/extract"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// ExampleChainUpdate locates an update mask nested in an Options
// sub-message, falling back to the canonical top-level update_mask, applies it
// against the stored resource (AIP-203: the output-only create_time is
// ignored, the identifier compared) and writes the cleaned mask back to the
// request.
func ExampleChainUpdate() {
	extractor := extract.ChainUpdate(
		extract.NewUpdateFunc(
			func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
			func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
			func(r *pb.NestedUpdateResourceRequest, m *fieldmaskpb.FieldMask) { r.Options.UpdateMask = m },
		),
		extract.DefaultUpdate(),
	)

	req := &pb.NestedUpdateResourceRequest{
		Resource: &pb.Resource{Id: "r1", Name: "renamed", CreateTime: "2026-01-01T00:00:00Z"},
		Options: &pb.NestedUpdateResourceRequest_Options{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "create_time"}},
		},
	}

	raw, resource, writeback, ok := extractor(context.Background(), req)
	if !ok {
		fmt.Println("no update mask on this request")

		return
	}

	stored := &pb.Resource{Id: "r1", Name: "original", CreateTime: "2025-01-01T00:00:00Z"} // read by the handler

	mask, err := fieldmask.FromProtoFieldMask(raw)
	if err != nil {
		fmt.Println(err)

		return
	}

	if err := update.Apply(mask, resource, update.WithCurrent(stored)); err != nil {
		fmt.Println(err)

		return
	}

	if writeback != nil {
		update.DropIdentifiers(mask, resource)
		writeback(mask.ToProtoFieldMask())
	}

	fmt.Println(req.GetOptions().GetUpdateMask().GetPaths())
	// Output: [name]
}

// ExampleChainRead locates a read mask nested in an Options
// sub-message and falls back to the top-level read_mask.
func ExampleChainRead() {
	read := extract.ChainRead(
		extract.NewReadFunc(func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask {
			return r.GetOptions().GetReadMask()
		}),
		extract.DefaultRead(), // top-level read_mask fallback
	)

	nested := &pb.NestedGetResourceRequest{
		Options: &pb.NestedGetResourceRequest_Options{ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}}},
	}
	canonical := &pb.GetResourceRequest{Name: "r1", ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}}}

	for _, req := range []proto.Message{nested, canonical} {
		mask, ok := read(context.Background(), req)
		fmt.Println(mask.GetPaths(), ok)
	}
	// Output:
	// [name] true
	// [description] true
}

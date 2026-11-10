// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// BenchmarkApplyMask measures ApplyMask on a typical client mask, a covered
// path included, against BenchmarkApplyUpdateMask_HappyPath.
func BenchmarkApplyMask(b *testing.B) {
	raw := &fieldmaskpb.FieldMask{Paths: []string{"name", "description", "profile", "profile.display_name"}}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = update.ApplyMask(raw, &pb.Resource{
			Name:        "n",
			Description: "d",
			Profile:     &pb.Profile{DisplayName: "p"},
		})
	}
}

// BenchmarkApplyMask_CoveredDuplicates measures a mask repeating a covered
// path up to the path limit under an IMMUTABLE parent with the stored
// resource: the covered checks must stay one per distinct path.
func BenchmarkApplyMask_CoveredDuplicates(b *testing.B) {
	paths := append([]string{"owner"}, slices.Repeat([]string{"owner.note"}, 999)...)
	raw := &fieldmaskpb.FieldMask{Paths: paths}
	req := &pb.Account{Key: make([]byte, 64<<10), Owner: &pb.Owner{Email: "e", Note: "n"}}
	opts := []update.Option{update.WithImmutableFields("owner"), update.WithCurrent(proto.Clone(req))}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = update.ApplyMask(raw, proto.Clone(req), opts...)
	}
}

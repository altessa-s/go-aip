// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

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

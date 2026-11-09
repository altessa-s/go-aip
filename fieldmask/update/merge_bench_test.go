// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkFromSetFieldsImplied(b *testing.B) {
	msg := &pb.Resource{Id: "r1", Name: "n", Description: "d", Profile: &pb.Profile{DisplayName: "p"}}

	b.ReportAllocs()

	for b.Loop() {
		_ = fieldmask.FromSetFields[[]string](msg,
			fieldmask.WithTopLevelOnly(true), fieldmask.WithDeclarationOrder(true), fieldmask.WithSkipFields("id"))
	}
}

func BenchmarkResolve(b *testing.B) {
	md := (&pb.Resource{}).ProtoReflect().Descriptor()
	mask := fieldmask.FromPaths("tenantId", "profile.displayName", "aliases.*.id")

	b.ReportAllocs()

	for b.Loop() {
		if _, err := mask.Resolve(md); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMerge(b *testing.B) {
	src := &pb.Resource{Name: "new", Description: "d", Profile: &pb.Profile{DisplayName: "p"}}
	mask := fieldmask.FromPaths("name", "description", "profile")

	b.ReportAllocs()

	for b.Loop() {
		dst := &pb.Resource{Id: "r1", Name: "old"}
		if err := update.Merge(mask, dst, src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelect(b *testing.B) {
	mask := fieldmask.FromPaths("name", "profile.display_name")

	b.ReportAllocs()

	for b.Loop() {
		msg := &pb.Resource{Id: "r1", Name: "n", Profile: &pb.Profile{DisplayName: "p", Secret: "s"}}
		mask.Select(msg)
	}
}

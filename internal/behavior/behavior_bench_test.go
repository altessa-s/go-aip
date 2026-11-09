// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/internal/behavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkResolveAnnotations(b *testing.B) {
	fd := (&pb.Resource{}).ProtoReflect().Descriptor().Fields().Get(0)

	b.ReportAllocs()

	for b.Loop() {
		behavior.Get(fd)
	}
}

func BenchmarkSubtree(b *testing.B) {
	md := (&pb.Resource{}).ProtoReflect().Descriptor()

	b.ReportAllocs()

	for b.Loop() {
		behavior.SubtreeHasAny(md, annotations.FieldBehavior_INPUT_ONLY)
	}
}

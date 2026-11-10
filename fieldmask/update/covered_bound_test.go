// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"bytes"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// coveredDuplicates returns parent followed by n copies of child.
func coveredDuplicates(parent, child string, n int) []string {
	return append([]string{parent}, slices.Repeat([]string{child}, n)...)
}

// TestCoveredPathsBoundedAllocations pins the exact case of a mask repeating
// a covered path up to the path limit over a large resource (1 MiB of bytes)
// with an IMMUTABLE message field and the stored resource: ["owner",
// "owner.note" x 999]. Every covered path is checked once and an IMMUTABLE comparison
// copies the compared field only, so the call allocates a small multiple of
// the resource instead of two copies of it per path (about 2 GB here).
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestCoveredPathsBoundedAllocations(t *testing.T) { //nolint:paralleltest // see above
	req := &pb.Account{Key: bytes.Repeat([]byte{1}, 1<<20), Owner: &pb.Owner{Email: "e", Note: "n"}}
	stored := proto.Clone(req)
	paths := coveredDuplicates("owner", "owner.note", 999)

	run := func(apply func() error) uint64 {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)
		require.NoError(t, apply())
		runtime.ReadMemStats(&after)

		return after.TotalAlloc - before.TotalAlloc
	}

	opts := []update.Option{update.WithImmutableFields("owner"), update.WithCurrent(stored)}

	allocated := run(func() error {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), opts...)
		return err
	})
	require.Less(t, allocated, uint64(64<<20), "ApplyMask allocated %d bytes", allocated)

	spec, err := update.NewSpec(&pb.Account{}, update.WithImmutableFields("owner"))
	require.NoError(t, err)

	allocated = run(func() error {
		_, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
		return err
	})
	require.Less(t, allocated, uint64(64<<20), "Spec.Plan allocated %d bytes", allocated)
}

// TestCoveredPathsCheckedOnce pins the exact case of a collection named whole
// and its whole elements repeated, ["members", "members.*" x 999], over
// elements whose REQUIRED field is empty: each element is reported once.
func TestCoveredPathsCheckedOnce(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Members: slices.Repeat([]*pb.Owner{{Org: "o"}}, 50)}
	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: coveredDuplicates("members", "members.*", 999)}, req)
	require.Len(t, behaviorViolations(t, err), 50)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"bytes"
	"fmt"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// coveredDuplicates returns parent followed by n copies of child.
func coveredDuplicates(parent, child string, n int) []string {
	return append([]string{parent}, slices.Repeat([]string{child}, n)...)
}

// TestCoveredPathsBoundedAllocations uses distinct covered paths and a large
// unselected field. The complete merge effect is evaluated once, so copies of
// that unrelated payload cost a small multiple of its size per call, rather
// than per covered path. Duplicate paths alone would miss that regression.
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestCoveredPathsBoundedAllocations(t *testing.T) { //nolint:paralleltest // see above
	req := &pb.Account{Key: bytes.Repeat([]byte{1}, 1<<20), Delegates: map[string]*pb.Owner{}}
	paths := []string{"delegates"}

	for i := range 128 {
		key := fmt.Sprintf("k%d", i)
		req.Delegates[key] = &pb.Owner{Email: "e", Org: "o", Note: "n"}
		paths = append(paths, "delegates."+key+".org")
	}

	stored := proto.Clone(req)

	run := func(apply func() error) uint64 {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)
		require.NoError(t, apply())
		runtime.ReadMemStats(&after)

		return after.TotalAlloc - before.TotalAlloc
	}

	opts := []update.Option{update.WithImmutableFields("delegates"), update.WithCurrent(stored)}

	allocated := run(func() error {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), opts...)
		return err
	})
	t.Logf("ApplyMask allocated %d bytes", allocated)
	require.Less(t, allocated, uint64(64<<20), "ApplyMask allocated %d bytes", allocated)

	spec, err := update.NewSpec(&pb.Account{}, update.WithImmutableFields("delegates"))
	require.NoError(t, err)

	allocated = run(func() error {
		_, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
		return err
	})
	t.Logf("Spec.Plan allocated %d bytes", allocated)
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

// Rejected updates additionally run the reference walk. Its sparse comparison
// holders still copy the selected protected subtree for each distinct covered
// selection, so the budget here scales with that payload and path count.
func TestRejectedCoveredPathsAllocations(t *testing.T) { //nolint:paralleltest // process-wide allocation counter
	const payloadBytes = 1 << 20

	original := &descriptorpb.FileDescriptorProto{Options: &descriptorpb.FileOptions{JavaPackage: new("new"),
		UninterpretedOption: []*descriptorpb.UninterpretedOption{{StringValue: bytes.Repeat([]byte{1}, payloadBytes)}}}}
	stored := proto.CloneOf(original)
	stored.Options.JavaPackage = new("old")
	options := []update.Option{update.WithAtomicMessage(nil), update.WithImmutableFields("options")}

	for _, covered := range []int{2, 12} {
		paths := []string{"options"}

		fields := original.Options.ProtoReflect().Descriptor().Fields()
		for i := range covered {
			paths = append(paths, "options."+string(fields.Get(i).Name()))
		}

		for _, entry := range []string{"ApplyMask", "Complete", "Spec"} {
			t.Run(fmt.Sprintf("%d/%s", covered, entry), func(t *testing.T) {
				req := proto.CloneOf(original)
				raw := maskOf(paths)
				spec, err := update.NewSpec(req, options...)
				require.NoError(t, err)

				var pending *update.DeferredPlan

				if entry == "Complete" {
					pending, err = update.Prepare(raw, req, options...)
					require.NoError(t, err)
				}

				var before, after runtime.MemStats

				runtime.GC()
				runtime.ReadMemStats(&before)

				switch entry {
				case "ApplyMask":
					_, err = update.ApplyMask(raw, req, append(slices.Clone(options), update.WithCurrent(stored))...)
				case "Complete":
					_, err = pending.Complete(stored)
				case "Spec":
					_, err = spec.Plan(req, raw, update.WithCurrent(stored))
				}

				runtime.ReadMemStats(&after)

				requireProtectedRefusal(t, err, entry)

				allocated := after.TotalAlloc - before.TotalAlloc
				t.Logf("%s with %d covered paths allocated %d bytes", entry, covered, allocated)
				require.Less(t, allocated, uint64((12+4*covered)*payloadBytes))
				requireProtoEqual(t, original, req)
				require.Equal(t, "old", stored.Options.GetJavaPackage())
			})
		}
	}
}

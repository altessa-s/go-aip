// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestRequiredWalkDepthFailsClosed pins the exact case of an Account whose
// parent chain is 5,000 levels deep under "*" and "parent": the REQUIRED walk
// stops at fieldbehavior.DefaultMaxDepth and the request is refused with
// fieldbehavior.ErrMaxDepthExceeded — never checked in part — by ApplyMask,
// Apply, ValidateStrict, Spec.Plan and fieldbehavior.ValidateRequired, each
// within a small allocation bound (the walk builds a path only for a
// violation).
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestRequiredWalkDepthFailsClosed(t *testing.T) { //nolint:paralleltest // see above
	deep := nestedParents(5000)

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	calls := map[string]func() error{
		"ApplyMask": func() error {
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, proto.Clone(deep))
			return err
		},
		"ApplyMask parent": func() error {
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"parent"}}, proto.Clone(deep))
			return err
		},
		"Apply": func() error { return update.Apply(fieldmask.FromPaths("*"), proto.Clone(deep)) },
		"ValidateStrict": func() error {
			_, err := update.ValidateStrict(fieldmask.FromPaths("parent"), deep)
			return err
		},
		"Spec.Plan": func() error {
			_, err := spec.Plan(deep, &fieldmaskpb.FieldMask{Paths: []string{"*"}})
			return err
		},
		"fieldbehavior.ValidateRequired": func() error { return fieldbehavior.ValidateRequired(deep) },
	}

	for name, call := range calls {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		err := call()

		runtime.ReadMemStats(&after)

		require.ErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded, name)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20), "%s allocated %d bytes", name, after.TotalAlloc-before.TotalAlloc)
	}
}

// TestRequiredWalkWithinDepth pins that a value within the limits is still
// checked in full: the REQUIRED key missing at the deepest level of an Account
// chain is reported by fieldbehavior.ValidateRequired at exactly
// fieldbehavior.DefaultMaxDepth levels, and by the update entry points just
// under the mask path depth limit (the expansion of a deeper chain is refused
// by that limit first). One level more is refused, never checked in part.
func TestRequiredWalkWithinDepth(t *testing.T) {
	t.Parallel()

	err := fieldbehavior.ValidateRequired(nestedParents(fieldbehavior.DefaultMaxDepth))
	require.ErrorIs(t, err, fieldbehavior.ErrBehaviorViolation)
	require.Contains(t, err.Error(), strings.Repeat("parent.", fieldbehavior.DefaultMaxDepth)+"key")

	require.ErrorIs(t, fieldbehavior.ValidateRequired(nestedParents(fieldbehavior.DefaultMaxDepth+1)), fieldbehavior.ErrMaxDepthExceeded)

	const levels = fieldmask.DefaultMaxPathDepth - 2

	deepest := strings.Repeat("parent.", levels) + "key"

	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, nestedParents(levels))
	require.Contains(t, violationPaths(t, err), deepest)

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	_, err = spec.Plan(nestedParents(levels), &fieldmaskpb.FieldMask{Paths: []string{"parent"}})
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

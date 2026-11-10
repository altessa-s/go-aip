// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// requireDepthRefusal asserts that err refuses a value or a mask for its
// depth: the payload depth sentinel or the mask path depth one.
func requireDepthRefusal(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()

	require.True(t, errors.Is(err, fieldbehavior.ErrMaxDepthExceeded) || errors.Is(err, fieldmask.ErrMaxPathDepthExceeded),
		append([]any{"want a depth refusal, got %v", err}, msgAndArgs...)...)
}

// TestImpliedMaskOverDeepPayload pins the exact case of an omitted mask over
// an Account whose parent chain is 5,000 levels deep: the synthesis of the
// implied mask stops at the path depth limit, the deepest path standing for
// the subtree below, which is then refused for its depth — never erased or
// built at a cost cubic in its depth — by ApplyMask (omitted, and empty with
// WithEmptyMaskImplied), Apply and ValidateStrict, each within a small
// allocation bound.
//
// Not parallel: it reads the process-wide allocation counter, which parallel
// tests would disturb (parallel tests wait until the serial ones are done).
func TestImpliedMaskOverDeepPayload(t *testing.T) { //nolint:paralleltest // see above
	deep := nestedParents(5000)

	calls := map[string]func() error{
		"ApplyMask omitted": func() error {
			_, err := update.ApplyMask(nil, proto.Clone(deep))
			return err
		},
		"ApplyMask empty implied": func() error {
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{}, proto.Clone(deep), update.WithEmptyMaskImplied(true))
			return err
		},
		"Apply": func() error { return update.Apply(nil, proto.Clone(deep)) },
		"ValidateStrict": func() error {
			_, err := update.ValidateStrict(nil, deep)
			return err
		},
	}

	for name, call := range calls {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		err := call()

		runtime.ReadMemStats(&after)

		requireDepthRefusal(t, err, name)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20), "%s allocated %d bytes", name, after.TotalAlloc-before.TotalAlloc)
	}
}

// TestImpliedMaskDeepImmutable pins that an IMMUTABLE value set below the path
// depth limit is not routed around by an omitted mask: an Account chain 35
// levels deep whose deepest region is tampered with is refused, with and
// without the stored resource, instead of having its deep subtree dropped
// from the implied mask.
func TestImpliedMaskDeepImmutable(t *testing.T) {
	t.Parallel()

	build := func(region string) *pb.Account {
		a := nestedParents(35)
		last := a

		for last.GetParent() != nil {
			last = last.GetParent()
		}

		last.Region = region

		return a
	}

	for _, opts := range [][]update.Option{nil, {update.WithCurrent(build("orig"))}} {
		_, err := update.ApplyMask(nil, build("tampered"), opts...)
		require.Error(t, err)

		require.Error(t, update.Apply(nil, build("tampered"), opts...))

		_, err = update.ValidateStrict(nil, build("tampered"), opts...)
		require.Error(t, err)
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

// TestImpliedMaskProtectedAtomicLeaf pins the exact cases of an omitted mask
// over a wrapper holding a protected atomic owner and a sibling w: the
// implied mask names a set owner on its own (["wrapper.owner"]) and leaves an
// unset one out (["wrapper.w"]), never the wrapper whole, so an update of w
// is not refused for the absent owner and an update of owner keeps the
// stored w, in ApplyMask, Apply, ValidateStrict, Spec.Plan and Patch.Run.
func TestImpliedMaskProtectedAtomicLeaf(t *testing.T) {
	t.Parallel()

	md := atomicLeafDescriptor(t)
	parse := func(text string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}
	atomic := atomicNames("atomicleaf.Atomic")
	stored := parse(`wrapper { owner { org: "o" note: "old" } w: "old" }`)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), atomic)
	require.NoError(t, err)

	for _, tc := range []struct{ name, req, path, want string }{
		{name: "sibling only", req: `wrapper { w: "new" }`, path: "wrapper.w", want: `wrapper { owner { org: "o" note: "old" } w: "new" }`},
		{name: "atomic only", req: `wrapper { owner { org: "o" note: "new" } }`, path: "wrapper.owner",
			want: `wrapper { owner { org: "o" note: "new" } w: "old" }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := parse(tc.req)
			opts := []update.Option{atomic, update.WithCurrent(stored)}

			require.Equal(t, []string{tc.path}, update.FromSetFields(req, atomic).ToPaths())

			msg := proto.Clone(req)
			got, err := update.ApplyMask(nil, msg, opts...)
			require.NoError(t, err)
			require.Equal(t, []string{tc.path}, got.ToPaths())

			merged := proto.Clone(stored)
			require.NoError(t, update.MergePaths(merged, msg, got.ToPaths(), atomic))
			requireProtoEqual(t, parse(tc.want), merged)

			require.NoError(t, update.Apply(nil, proto.Clone(req), opts...))

			strict, err := update.ValidateStrict(nil, req, opts...)
			require.NoError(t, err)
			require.Equal(t, []string{tc.path}, strict.ToPaths())

			plan, err := spec.Plan(req, nil, update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, []string{tc.path}, plan.Paths)

			var saved *dynamicpb.Message

			patch := update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64]{
				Field: "root", Spec: spec,
				Precondition: update.Int64Precondition[*dynamicpb.Message]{Field: "root.etag", Version: func(*dynamicpb.Message) int64 { return 1 }},
				Read: func(context.Context) (*dynamicpb.Message, error) {
					return proto.Clone(stored).(*dynamicpb.Message), nil
				},
				ToProto: func(m *dynamicpb.Message) *dynamicpb.Message { return m },
				Save: func(_ context.Context, m *dynamicpb.Message, _ update.Plan, _ *int64) (*dynamicpb.Message, error) {
					saved = m
					return m, nil
				},
			}
			_, err = patch.Run(t.Context(), req, "", nil)
			require.NoError(t, err)
			requireProtoEqual(t, parse(tc.want), saved)
		})
	}

	// A set owner changing its IMMUTABLE org is still refused.
	_, err = update.ApplyMask(nil, parse(`wrapper { owner { org: "changed" } }`), atomic, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

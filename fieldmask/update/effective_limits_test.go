// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestEffectiveMaskLimits pins that the limits bound the effective mask:
// the exact case of ["profile"] expanding to profile.display_name and
// profile.secret under WithMaxPaths(1), the same expansion under
// WithMaxPathDepth(1), and "*" expanding past WithMaxPaths(2), are refused
// by ApplyMask, Apply, ValidateStrict and Spec.Plan with the limit sentinel,
// before the request is touched, instead of returning paths MergePaths
// refuses.
func TestEffectiveMaskLimits(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{Profile: &pb.Profile{DisplayName: "n"}, Description: "d"}

	for _, tc := range []struct {
		name  string
		path  string
		limit update.Option
		cause error
	}{
		{name: "count", path: "profile", limit: update.WithMaxPaths(1), cause: fieldmask.ErrMaxPathsExceeded},
		{name: "depth", path: "profile", limit: update.WithMaxPathDepth(1), cause: fieldmask.ErrMaxPathDepthExceeded},
		{name: "full replacement", path: "*", limit: update.WithMaxPaths(2), cause: fieldmask.ErrMaxPathsExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := proto.Clone(req)
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.path}}, msg, tc.limit)
			require.ErrorIs(t, err, tc.cause)
			requireProtoEqual(t, req, msg)

			msg = proto.Clone(req)
			require.ErrorIs(t, update.Apply(fieldmask.FromPaths(tc.path), msg, tc.limit), tc.cause)
			requireProtoEqual(t, req, msg)

			_, err = update.ValidateStrict(fieldmask.FromPaths(tc.path), req, tc.limit)
			require.ErrorIs(t, err, tc.cause)

			spec, err := update.NewSpec(&pb.Resource{}, tc.limit)
			require.NoError(t, err)

			_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{tc.path}})
			require.ErrorIs(t, err, tc.cause)
		})
	}

	// Within the limits the expansion is returned and merges.
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"profile"}}, proto.Clone(req), update.WithMaxPaths(2))
	require.NoError(t, err)
	require.Equal(t, []string{"profile.display_name", "profile.secret"}, got.ToPaths())
	require.NoError(t, update.MergePaths(&pb.Resource{}, req, got.ToPaths(), update.WithMaxPaths(2)))
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestValidateStrictUpdateMask_CollectionElements pins the strict check of paths into the elements of a repeated message field
// and the values of a message map: unverified protected paths are dropped without an error, while mutable paths remain.
func TestValidateStrictUpdateMask_CollectionElements(t *testing.T) {
	t.Parallel()

	resource := &pb.Resource{
		Aliases: []*pb.Profile{{DisplayName: "a"}},
		Labels:  map[string]*pb.Profile{"k": {DisplayName: "b"}},
	}

	for _, tc := range []struct {
		name    string
		paths   []string
		dropped bool
	}{
		{name: "map identifier", paths: []string{"labels.*.id"}, dropped: true},
		{name: "list mutable", paths: []string{"aliases.*.display_name"}},
		{name: "map mutable", paths: []string{"labels.*.display_name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mask, err := update.ValidateStrict(fieldmask.FromPaths(tc.paths...), resource)
			if !tc.dropped {
				require.NoError(t, err)
				require.Equal(t, fieldmask.FromPaths(tc.paths...), mask)

				return
			}

			require.NoError(t, err)
			require.Empty(t, mask)
		})
	}
}

// TestValidateStrictUpdateMask_OutputOnlyDropped pins that an OUTPUT_ONLY path, top-level or in collection elements, is
// dropped from the returned mask and never reported.
func TestValidateStrictUpdateMask_OutputOnlyDropped(t *testing.T) {
	t.Parallel()

	resource := &pb.Resource{Aliases: []*pb.Profile{{DisplayName: "a"}}}
	mask, err := update.ValidateStrict(fieldmask.FromPaths("aliases.*.updated_at", "create_time", "description"), resource)
	require.NoError(t, err)
	require.Equal(t, []string{"description"}, mask.ToPaths())
}

// TestStrictUpdateMaskError_WithoutViolations pins the message of an error carrying only a cause.
func TestStrictUpdateMaskError_WithoutViolations(t *testing.T) {
	t.Parallel()

	require.Equal(t, "invalid update_mask", (&update.StrictError{}).Error())
}

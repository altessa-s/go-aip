// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestValidateStrictUpdateMask_CollectionElements pins the strict check of paths into the elements of a repeated message field
// and the values of a message map: an IDENTIFIER or IMMUTABLE field of the element is reported with its element path when the
// stored value is unknown, an OUTPUT_ONLY one is dropped from the returned mask without an error (AIP-203), a mutable one is
// accepted.
func TestValidateStrictUpdateMask_CollectionElements(t *testing.T) {
	t.Parallel()

	resource := &pb.Resource{
		Aliases: []*pb.Profile{{DisplayName: "a"}},
		Labels:  map[string]*pb.Profile{"k": {DisplayName: "b"}},
	}

	for _, tc := range []struct {
		name   string
		paths  []string
		reason string
		path   string
	}{
		{name: "map identifier", paths: []string{"labels.*.id"}, path: "labels.*.id",
			reason: "identifier field cannot be modified"},
		{name: "list mutable", paths: []string{"aliases.*.display_name"}},
		{name: "map mutable", paths: []string{"labels.*.display_name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mask, err := update.ValidateStrict(fieldmask.FromPaths(tc.paths...), resource)
			if tc.reason == "" {
				require.NoError(t, err)
				require.Equal(t, fieldmask.FromPaths(tc.paths...), mask)

				return
			}

			var strict *update.StrictError
			require.True(t, errors.As(err, &strict), "%v", err)
			require.Len(t, strict.Violations, 1)
			require.Equal(t, tc.path, strict.Violations[0].Path)
			require.Equal(t, tc.reason, strict.Violations[0].Reason)
			require.Equal(t, `invalid update_mask path "`+tc.path+`": `+tc.reason, err.Error())
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

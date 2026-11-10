// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	sdpb "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
)

// TestAtomicDescentRefusedInEveryMode pins that a sub-path into an atomic
// message is refused under PathValidationWarn and PathValidationDisabled
// even when its tail is not a field ("delete_time.typo", "delete_time.*",
// the JSON spelling "deleteTime.typo"), instead of being dropped while the
// atomic value is cleared.
func TestAtomicDescentRefusedInEveryMode(t *testing.T) {
	t.Parallel()

	draft := &sdpb.Draft{DeleteTime: timestamppb.New(time.Unix(1, 0))}

	for _, mode := range []update.PathValidationMode{update.PathValidationWarn, update.PathValidationDisabled} {
		for _, path := range []string{"delete_time.typo", "delete_time.*", "deleteTime.typo", "delete_time.seconds.x"} {
			msg := proto.Clone(draft)
			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, msg, update.WithPathValidation(mode))
			requireValidationPath(t, err, "delete_time")
			requireProtoEqual(t, draft, msg)

			msg = proto.Clone(draft)
			err = update.Apply(fieldmask.FromPaths(path), msg, update.WithPathValidation(mode))
			requireValidationPath(t, err, "delete_time")
			requireProtoEqual(t, draft, msg)
		}
	}

	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delete_time.typo"}}, proto.Clone(draft))
	requireValidationPath(t, err, "delete_time.typo") // Enforce reports the path as sent

	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delete_time", "no_such"}}, proto.Clone(draft),
		update.WithPathValidation(update.PathValidationDisabled))
	require.NoError(t, err)
	require.Equal(t, []string{"delete_time"}, got.ToPaths(), "an unknown path that enters no atomic value is still dropped")
}

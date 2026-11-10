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

	fipb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
)

// TestApplyReportsNoFormatViolation pins that clearing OUTPUT_ONLY values
// after the filter checks no AIP-202 format: a request whose kept uid is not
// a UUID4 is applied by ApplyMask and Apply as any other, never failing after
// the request was filtered with a fieldbehavior error and a half-mutated
// request. Formats are the payload checks of the fieldbehavior package.
func TestApplyReportsNoFormatViolation(t *testing.T) {
	t.Parallel()

	req := &fipb.Host{Uid: "not-a-uuid", ContactEmail: "keep", RequestId: "client"}

	msg := proto.Clone(req)
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"uid"}}, msg)
	require.NoError(t, err)
	require.Equal(t, []string{"uid"}, got.ToPaths())
	requireProtoEqual(t, &fipb.Host{Uid: "not-a-uuid"}, msg)

	msg = proto.Clone(req)
	require.NoError(t, update.Apply(fieldmask.FromPaths("uid"), msg))
	requireProtoEqual(t, &fipb.Host{Uid: "not-a-uuid"}, msg)
}

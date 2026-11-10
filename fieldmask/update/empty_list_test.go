// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestEmptyRequestListSubPath pins the exact case of a "*" sub-path of a list
// whose request holds no elements: stored Members=[{Email:"existing"}], an
// empty request, ["members.*.email"]. The merge updates every stored element
// from nothing and clears its REQUIRED email, so ApplyMask, Apply,
// ValidateStrict, Spec.Plan and Patch.Run refuse it. A sub-path to an
// optional field is accepted, and Patch clears it in every stored element.
func TestEmptyRequestListSubPath(t *testing.T) {
	t.Parallel()

	stored := &pb.Account{Members: []*pb.Owner{{Email: "existing@example.com", Note: "n"}, {Email: "other@example.com", Note: "m"}}}
	want := []FieldViolation{
		{Path: "members.0.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired},
		{Path: "members.1.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired},
	}

	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"members.*.email"}}, &pb.Account{}, update.WithCurrent(stored))
	require.Equal(t, want, behaviorViolations(t, err))

	err = update.Apply(fieldmask.FromPaths("members.*.email"), &pb.Account{}, update.WithCurrent(stored))
	require.Equal(t, want, behaviorViolations(t, err))

	_, err = update.ValidateStrict(fieldmask.FromPaths("members.*.email"), &pb.Account{}, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	_, err = spec.Plan(&pb.Account{}, &fieldmaskpb.FieldMask{Paths: []string{"members.*.email"}}, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)

	s := &accountStore{cur: proto.Clone(stored).(*pb.Account)}
	_, err = s.patch(t).Run(t.Context(), &pb.Account{}, "", &fieldmaskpb.FieldMask{Paths: []string{"members.*.email"}})
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.Nil(t, s.saved, "nothing saved")

	// An optional field may be cleared in every stored element.
	out, err := s.patch(t).Run(t.Context(), &pb.Account{}, "", &fieldmaskpb.FieldMask{Paths: []string{"members.*.note"}})
	require.NoError(t, err)
	requireProtoEqual(t, &pb.Account{Members: []*pb.Owner{{Email: "existing@example.com"}, {Email: "other@example.com"}}}, out)

	// Replacing the list whole stays a replacement: the empty request clears it.
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"members"}}, &pb.Account{}, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"members"}, got.ToPaths())
}

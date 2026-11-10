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

// TestLiteralStarMapKey pins that a request map entry whose literal key is
// "*" is an ordinary entry that only the wildcard reaches (a mask path cannot
// name it, AIP-161): "delegates.*" checks its REQUIRED fields, a "*"
// sub-path checks and compares it, the merge updates it, and a wildcard that
// would have to be pinned to name it is refused.
func TestLiteralStarMapKey(t *testing.T) {
	t.Parallel()

	star := func(email, org string) *pb.Account {
		return &pb.Account{Delegates: map[string]*pb.Owner{"*": {Email: email, Org: org}}}
	}

	t.Run("whole elements", func(t *testing.T) {
		t.Parallel()

		want := []FieldViolation{
			{Path: "delegates.*.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired},
			{Path: "delegates.*.org", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired},
		}

		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delegates.*"}}, star("", ""), requiredImmutableOrg)
		require.Equal(t, want, behaviorViolations(t, err))

		err = update.Apply(fieldmask.FromPaths("delegates.*"), star("", ""), requiredImmutableOrg)
		require.Equal(t, want, behaviorViolations(t, err))

		_, err = update.ValidateStrict(fieldmask.FromPaths("delegates.*"), star("", ""), requiredImmutableOrg)
		require.ErrorIs(t, err, update.ErrBehaviorViolation)

		spec, err := update.NewSpec(&pb.Account{}, requiredImmutableOrg)
		require.NoError(t, err)

		_, err = spec.Plan(star("", ""), &fieldmaskpb.FieldMask{Paths: []string{"delegates.*"}})
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})

	t.Run("sub-path REQUIRED", func(t *testing.T) {
		t.Parallel()

		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delegates.*.email"}}, star("", "o"), update.WithCurrent(star("x", "o")))
		require.Equal(t, []FieldViolation{{Path: "delegates.*.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}},
			behaviorViolations(t, err))
	})

	t.Run("unnamed IDENTIFIER compared", func(t *testing.T) {
		t.Parallel()

		stored := &pb.Resource{Labels: map[string]*pb.Profile{"*": {Id: "real", DisplayName: "old"}}}
		req := &pb.Resource{Labels: map[string]*pb.Profile{"*": {Id: "spoofed", DisplayName: "new"}}}
		want := []FieldViolation{{Path: "labels.*.id", Behavior: annotations.FieldBehavior_IDENTIFIER,
			Reason: "identifier field cannot be changed from its stored value"}}

		msg := proto.Clone(req)
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"labels.*.display_name"}}, msg, update.WithCurrent(stored))
		require.Equal(t, want, behaviorViolations(t, err))
		requireProtoEqual(t, req, msg)

		err = update.Apply(fieldmask.FromPaths("labels.*.display_name"), proto.Clone(req), update.WithCurrent(stored))
		require.Equal(t, want, behaviorViolations(t, err))

		spec, err := update.NewSpec(&pb.Resource{})
		require.NoError(t, err)

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"labels.*.display_name"}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrProtectedField)
	})

	t.Run("merge reaches it through the wildcard", func(t *testing.T) {
		t.Parallel()

		merged := star("old@example.com", "o")
		require.NoError(t, update.MergePaths(merged, star("new@example.com", "o"), []string{"delegates.*.email"}))
		requireProtoEqual(t, star("new@example.com", "o"), merged)

		spec, err := update.NewSpec(&pb.Account{})
		require.NoError(t, err)

		plan, err := spec.Plan(star("new@example.com", "o"), &fieldmaskpb.FieldMask{Paths: []string{"delegates.*.email"}},
			update.WithCurrent(star("old@example.com", "o")))
		require.NoError(t, err)
		require.Equal(t, []string{"delegates.*.email"}, plan.Paths)
	})

	t.Run("unpinnable", func(t *testing.T) {
		t.Parallel()

		stored := delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}, "*": {"s@example.com", "so"}})
		req := delegatesAccount(map[string][2]string{"k": {"", "ko"}, "*": {"new@example.com", "so"}})
		paths := []string{"delegates.*.email", "delegates.k.org"}

		msg := proto.Clone(req)
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, msg, update.WithCurrent(stored))
		requireValidationPath(t, err, "delegates.*")
		requireProtoEqual(t, req, msg)

		spec, err := update.NewSpec(&pb.Account{})
		require.NoError(t, err)

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
		require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
	})
}

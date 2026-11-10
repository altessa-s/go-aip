// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestReplaceAllSkipsDeepPolicyProtectedField pins the exact case of a full
// replacement that echoes an unchanged parent the policy makes immutable
// (WithImmutableFields("parent")) nested 40 levels deep: the replacement
// never writes it, so its REQUIRED walk skips it before descending and the
// request is accepted by ApplyMask, Apply, ValidateStrict and Spec.Plan,
// instead of being refused for its depth. A changed parent is still refused
// by the comparison.
func TestReplaceAllSkipsDeepPolicyProtectedField(t *testing.T) {
	t.Parallel()

	immutable := update.WithImmutableFields("parent")

	stored := storedAccount()
	stored.Parent = nestedParents(40)
	req := proto.Clone(stored).(*pb.Account)

	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, proto.Clone(req), update.WithCurrent(stored), immutable)
	require.NoError(t, err)

	require.NoError(t, update.Apply(fieldmask.FromPaths("*"), proto.Clone(req), update.WithCurrent(stored), immutable))

	_, err = update.ValidateStrict(fieldmask.FromPaths("*"), req, update.WithCurrent(stored), immutable)
	require.NoError(t, err)

	spec, err := update.NewSpec(&pb.Account{}, immutable)
	require.NoError(t, err)

	_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"*"}}, update.WithCurrent(stored))
	require.NoError(t, err)

	changed := proto.Clone(req).(*pb.Account)
	changed.Parent.DisplayName = "other"

	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, changed, update.WithCurrent(stored), immutable)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.NotErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded)
}

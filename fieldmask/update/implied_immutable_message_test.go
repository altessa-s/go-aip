// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestImpliedMaskImmutableMessage pins the exact cases of an omitted mask
// whose request sets a message the policy makes immutable
// (WithImmutableFields("owner")): the implied mask names owner whole, so an
// empty Owner{} and an Owner{Email} clearing the stored org and note are
// retained without current state and refused as changed with it, by
// ApplyMask, Apply and ValidateStrict. The value is compared whole, not
// through owner.email alone. An unchanged owner is ignored.
func TestImpliedMaskImmutableMessage(t *testing.T) {
	t.Parallel()

	immutable := update.WithImmutableFields("owner")
	stored := &pb.Account{Owner: &pb.Owner{Email: "a@b.com", Org: "corp", Note: "vip"}}

	for _, owner := range []*pb.Owner{{}, {Email: "a@b.com"}} {
		req := &pb.Account{DisplayName: "new", Owner: owner}
		require.Contains(t, update.FromSetFields(req, immutable).ToPaths(), "owner")

		for _, opts := range [][]update.Option{{immutable, update.WithCurrent(stored)}} {
			_, err := update.ApplyMask(nil, proto.Clone(req), opts...)
			require.ErrorIs(t, err, update.ErrBehaviorViolation)

			require.ErrorIs(t, update.Apply(nil, proto.Clone(req), opts...), update.ErrBehaviorViolation)

			_, err = update.ValidateStrict(nil, req, opts...)
			require.ErrorIs(t, err, update.ErrBehaviorViolation)
		}
	}

	req := &pb.Account{DisplayName: "new", Owner: proto.Clone(stored.GetOwner()).(*pb.Owner)}
	got, err := update.ApplyMask(nil, req, immutable, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"display_name"}, got.ToPaths())
}

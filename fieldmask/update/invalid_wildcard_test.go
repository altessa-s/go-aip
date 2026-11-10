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

// TestInvalidWildcardKeepsValidSiblings pins the exact case of a "*" the
// schema rejects ("owner.*", owner a singular message) beside a valid sibling
// ("owner.email") under PathValidationWarn and PathValidationDisabled: the
// invalid path matches no field and never covers the valid one, so the
// REQUIRED email the request sets is kept, in Apply and ApplyMask alike.
func TestInvalidWildcardKeepsValidSiblings(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Owner: &pb.Owner{Email: "alice@example.com"}}

	for _, mode := range []update.PathValidationMode{update.PathValidationWarn, update.PathValidationDisabled} {
		msg := proto.Clone(req)
		mask := fieldmask.FromPaths("owner.email", "owner.*")
		require.NoError(t, update.Apply(mask, msg, update.WithPathValidation(mode)))
		requireProtoEqual(t, req, msg)
		require.Contains(t, mask.ToPaths(), "owner.email")

		msg = proto.Clone(req)
		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"owner.email", "owner.*"}}, msg, update.WithPathValidation(mode))
		require.NoError(t, err)
		require.Equal(t, []string{"owner.email"}, got.ToPaths())
		requireProtoEqual(t, req, msg)
	}
}

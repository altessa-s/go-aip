// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestMaskSpecRejectsMismatchedResource(t *testing.T) {
	t.Parallel()

	spec, specErr := update.NewSpec(&pb.Resource{})
	require.NoError(t, specErr)

	for _, msg := range []*emptypb.Empty{nil, {}} {
		_, err := spec.Plan(msg, nil)
		require.ErrorIs(t, err, update.ErrMessageMismatch)
	}

	_, err := spec.Plan(nil, nil)
	require.ErrorIs(t, err, update.ErrMessageMismatch)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestImpliedPlanComparisonBounds pins that the comparison of the protected
// values an implied plan's request sets emits no path, so the path limits do
// not apply to it and it visits only what the request holds: the exact case
// of Plan(&Resource{Description: "d"}, nil) under WithMaxPathDepth(1), with
// and without a stored profile, and an empty request over a schema branching
// at each of 24 levels, are planned; a protected value the request sets is
// still compared.
func TestImpliedPlanComparisonBounds(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Resource{}, update.WithMaxPathDepth(1))
	require.NoError(t, err)

	for _, opts := range [][]update.Option{nil, {update.WithCurrent(&pb.Resource{Profile: &pb.Profile{Id: "p", DisplayName: "n"}})}} {
		plan, err := spec.Plan(&pb.Resource{Description: "d"}, nil, opts...)
		require.NoError(t, err)
		require.Equal(t, []string{"description"}, plan.Paths)
	}

	_, err = spec.Plan(&pb.Resource{Description: "d", TenantId: "t"}, nil)
	require.NoError(t, err, "a set IMMUTABLE value without current state is deferred")

	md := branchingDescriptor(t, branchingLevels)
	branching, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	plan, err := branching.Plan(dynamicpb.NewMessage(md), nil)
	require.NoError(t, err)
	require.True(t, plan.Empty())
}

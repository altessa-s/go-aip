// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestImpliedPlanAtomicAbsentValue pins the exact case of an implied plan
// whose request leaves an atomic value unset: Plan(Resource{Description:
// "new"}, nil) with Profile atomic is planned with and without a stored
// profile (Patch.Run plans the same way), since the comparison writes nothing
// and the absent profile is not replaced; a profile the request supplies with
// a changed IDENTIFIER is still refused, implied or named.
func TestImpliedPlanAtomicAbsentValue(t *testing.T) {
	t.Parallel()

	atomic := atomicNames("fieldbehaviortest.v1.Profile")

	spec, err := update.NewSpec(&pb.Resource{}, atomic)
	require.NoError(t, err)

	stored := &pb.Resource{Description: "old", Profile: &pb.Profile{Id: "p"}}

	for _, opts := range [][]update.Option{nil, {update.WithCurrent(stored)}} {
		plan, err := spec.Plan(&pb.Resource{Description: "new"}, nil, opts...)
		require.NoError(t, err)
		require.Equal(t, []string{"description"}, plan.Paths)
	}

	_, err = spec.Plan(&pb.Resource{Description: "new", Profile: &pb.Profile{Id: "other"}}, nil, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrProtectedField)

	_, err = spec.Plan(&pb.Resource{Description: "new", Profile: &pb.Profile{Id: "other"}},
		&fieldmaskpb.FieldMask{Paths: []string{"profile"}}, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrProtectedField)
}

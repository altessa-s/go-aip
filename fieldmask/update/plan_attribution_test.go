// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// planRefusalIndex returns the mask index a Spec.Plan refusal is located at.
func planRefusalIndex(t *testing.T, err error) int {
	t.Helper()

	mp, ok := errors.AsType[*update.MaskPathError](err)
	require.True(t, ok, "%v", err)

	return mp.Index
}

// TestPlanRefusalAttribution pins that Spec.Plan locates a refusal at the
// mask path it concerns: a limit of the whole effective mask on the bare mask
// field (index -1), not the explicit path with the most segments; and a
// refusal of the entries a "*" sub-path addresses on that path, not on a key
// path beside it naming another entry — a changed IMMUTABLE value under
// "delegates.*.org" beside an unchanged "delegates.k.org", and a protected
// oneof switch under "items.*.free" beside "items.k.free".
func TestPlanRefusalAttribution(t *testing.T) {
	t.Parallel()

	t.Run("mask-level limit", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(&pb.Resource{}, update.WithMaxPaths(2))
		require.NoError(t, err)

		_, err = spec.Plan(&pb.Resource{Description: "d", Profile: &pb.Profile{DisplayName: "n"}},
			&fieldmaskpb.FieldMask{Paths: []string{"profile", "description"}})
		require.ErrorIs(t, err, fieldmask.ErrMaxPathsExceeded)
		require.Equal(t, -1, planRefusalIndex(t, err))
	})

	t.Run("wildcard violation beside a key", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(&pb.Account{})
		require.NoError(t, err)

		stored := delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}, "j": {"j@example.com", "jo"}})
		req := delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}, "j": {"j@example.com", "changed"}})

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"delegates.k.org", "delegates.*.org"}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrProtectedField)
		require.Equal(t, 1, planRefusalIndex(t, err))
	})

	t.Run("wildcard oneof switch beside a key", func(t *testing.T) {
		t.Parallel()

		md := collectionDescriptor(t)
		stored := collectionRoot(md, map[string]map[string]string{"k": {"free": "old"}, "j": {"pinned": "old"}}, nil, nil)
		req := collectionRoot(md, map[string]map[string]string{"k": {"free": "new"}, "j": {"free": "new"}}, nil, nil)

		spec, err := update.NewSpec(stored.New().Interface())
		require.NoError(t, err)

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"items.k.free", "items.*.free"}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrProtectedField)
		require.Equal(t, 1, planRefusalIndex(t, err))
	})
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// nestedParents returns an Account whose parent chain is depth levels deep.
func nestedParents(depth int) *pb.Account {
	a := &pb.Account{DisplayName: "leaf"}
	for range depth {
		a = &pb.Account{DisplayName: "d", Parent: a}
	}

	return a
}

// TestReplacementOverRecursivePayload pins that "*" and a message named whole
// over a recursive payload deeper than the path depth limit (an Account
// whose parent chain is 200 levels deep) stop expanding at the limit and
// are refused with fieldbehavior.ErrMaxDepthExceeded by ApplyMask, Apply,
// ValidateStrict, Spec.Plan and Patch.Run, the last as the field error of
// the mask before any merge; a payload within the limit is not refused for
// its depth.
func TestReplacementOverRecursivePayload(t *testing.T) {
	t.Parallel()

	deep := nestedParents(200)
	errDepth := fieldbehavior.ErrMaxDepthExceeded // the REQUIRED walk over the value refuses it first

	for _, path := range []string{"*", "parent"} {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, proto.Clone(deep))
		require.ErrorIs(t, err, errDepth, path)

		require.ErrorIs(t, update.Apply(fieldmask.FromPaths(path), proto.Clone(deep)), errDepth, path)

		_, err = update.ValidateStrict(fieldmask.FromPaths(path), deep)
		require.ErrorIs(t, err, errDepth, path)

		spec, err := update.NewSpec(&pb.Account{})
		require.NoError(t, err)

		_, err = spec.Plan(deep, &fieldmaskpb.FieldMask{Paths: []string{path}})
		require.ErrorIs(t, err, errDepth, path)

		s := &accountStore{cur: &pb.Account{DisplayName: "stored"}}
		_, err = s.patch(t).Run(t.Context(), deep, "", &fieldmaskpb.FieldMask{Paths: []string{path}})
		require.ErrorIs(t, err, errDepth, path)

		_, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok, "%v", err)
		require.Nil(t, s.saved)
	}

	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"parent"}}, nestedParents(3))
	require.NotErrorIs(t, err, fieldmask.ErrMaxPathDepthExceeded)
	require.NotErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded)
}

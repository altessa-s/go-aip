// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	maskpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func TestResolveMask(t *testing.T) {
	t.Parallel()

	resource := &pb.Resource{
		CreateTime: "server", Description: "unlisted",
		Profile: &pb.Profile{DisplayName: "display", Secret: "unlisted"}, Aliases: []*pb.Profile{{}},
	}
	spec, err := update.NewSpec(resource, update.WithAllowedPaths("name", "profile", "profile.display_name", "aliases"))
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		raw  *fieldmaskpb.FieldMask
		want []update.ResolvedPath
	}{
		{name: "empty required leaf", raw: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			want: []update.ResolvedPath{{Path: "name", Index: 0}}},
		{name: "omitted", want: []update.ResolvedPath{{Path: "profile.display_name", Index: -1}, {Path: "aliases", Index: -1}}},
		{name: "empty", raw: &fieldmaskpb.FieldMask{}},
		{name: "wildcard", raw: &fieldmaskpb.FieldMask{Paths: []string{"*"}},
			want: []update.ResolvedPath{{Path: "name", Index: -1}, {Path: "profile.display_name", Index: -1}, {Path: "aliases", Index: -1}}},
		{name: "duplicate and overlap", raw: &fieldmaskpb.FieldMask{Paths: []string{"profile", "profile.displayName", "profile.display_name"}},
			want: []update.ResolvedPath{{Path: "profile.display_name", Index: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			selection, err := spec.ResolveMask(resource, tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, selection.Paths())
		})
	}
}

func TestResolveMaskAbsentParent(t *testing.T) {
	t.Parallel()

	resource := &maskpb.UpdateRequest{}
	spec, err := update.NewSpec(resource)
	require.NoError(t, err)

	mask := &fieldmaskpb.FieldMask{Paths: []string{"shippingAddress"}}
	plan, err := spec.Plan(resource, mask)
	require.NoError(t, err)
	require.Equal(t, []string{"shipping_address"}, plan.Paths)

	resolved, err := spec.ResolveMask(resource, mask)
	require.NoError(t, err)
	require.Equal(t, []update.ResolvedPath{
		{Path: "shipping_address.city", Index: 0},
		{Path: "shipping_address.street", Index: 0},
		{Path: "shipping_address.zip", Index: 0},
	}, resolved.Paths())

	violations, err := spec.ValidateResolved(resource, resolved)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "shipping_address.city", violations[0].Path)
	require.Equal(t, annotations.FieldBehavior_REQUIRED, violations[0].Behavior)
}

func TestResolveMaskPathErrors(t *testing.T) {
	t.Parallel()

	resource := &pb.Resource{}
	spec, err := update.NewSpec(resource, update.WithAllowedPaths("description", "aliases"))
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		paths []string
		index int
		cause error
	}{
		{name: "output only", paths: []string{"description", "createTime"}, index: 1, cause: fieldmask.ErrInvalidPath},
		{name: "identity", paths: []string{"id"}, cause: fieldmask.ErrInvalidPath},
		{name: "list descent", paths: []string{"aliases.*.displayName"}, cause: fieldmask.ErrInvalidPath},
		{name: "unlisted collection wildcard", paths: []string{"aliases.*"}, cause: fieldmask.ErrInvalidPath},
		{name: "unknown", paths: []string{"missing"}, cause: fieldmask.ErrInvalidPath},
		{name: "star first", paths: []string{"*", "description"}, cause: fieldmask.ErrWildcardNotAlone},
		{name: "star last", paths: []string{"description", "*"}, index: 1, cause: fieldmask.ErrWildcardNotAlone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.ResolveMask(resource, &fieldmaskpb.FieldMask{Paths: tc.paths})
			require.ErrorIs(t, err, tc.cause)
			pathErr, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok)
			require.Equal(t, tc.index, pathErr.Index)
			require.Equal(t, tc.paths[tc.index], pathErr.Path)
		})
	}
}

func TestValidateResolvedListIndexes(t *testing.T) {
	t.Parallel()

	resource := &pb.Account{Members: []*pb.Owner{{Email: "first", Org: "old"}, {Org: "new"}}}
	spec, err := update.NewSpec(resource, update.WithAllowedPaths("members"))
	require.NoError(t, err)

	resolved, err := spec.ResolveMask(resource, &fieldmaskpb.FieldMask{Paths: []string{"members"}})
	require.NoError(t, err)
	require.Equal(t, []update.ResolvedPath{{Path: "members", Index: 0}}, resolved.Paths())

	current := &pb.Account{Members: []*pb.Owner{{Email: "first", Org: "old"}, {Email: "second", Org: "old"}}}
	violations, err := spec.ValidateResolved(resource, resolved, update.WithCurrent(current))
	require.NoError(t, err)
	require.Len(t, violations, 2)
	require.Equal(t, "members.email", violations[0].Path)
	require.Equal(t, []update.ListIndex{{Segment: 0, Index: 1}}, violations[0].ListIndexes)
	require.Equal(t, "members[1].email", violations[0].IndexedPath())
	require.Equal(t, annotations.FieldBehavior_REQUIRED, violations[0].Behavior)
	require.Equal(t, "members[1].org", violations[1].IndexedPath())
	require.Equal(t, annotations.FieldBehavior_IMMUTABLE, violations[1].Behavior)
}

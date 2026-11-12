// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TestPatchRunValidateOnly pins AIP-163: a validate_only update runs every
// check and returns the would-be resource without saving or creating.
func TestPatchRunValidateOnly(t *testing.T) {
	t.Parallel()

	validate := update.WithValidateOnly(true)
	mask := &fieldmaskpb.FieldMask{Paths: []string{"display_name"}}

	t.Run("returns the merged resource without saving", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		out, err := s.patch(t).Run(t.Context(), &pb.Account{DisplayName: "renamed", UpdateTime: "client"},
			etag.FromRevision[int64](1).String(), mask, validate)
		require.NoError(t, err)
		require.Nil(t, s.saved)

		want := storedAccount()
		want.DisplayName = "renamed"
		require.True(t, proto.Equal(want, out), "got %v", out)
	})
	t.Run("an empty mask returns the stored resource", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		out, err := s.patch(t).Run(t.Context(), &pb.Account{}, "", &fieldmaskpb.FieldMask{}, validate)
		require.NoError(t, err)
		require.Nil(t, s.saved)
		require.True(t, proto.Equal(storedAccount(), out))
	})
	t.Run("allow_missing returns the would-be resource without creating", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{}
		req := &pb.Account{Name: "accounts/9", DisplayName: "new", UpdateTime: "client"}
		out, err := s.patch(t).Run(t.Context(), req, "", mask, validate, update.WithAllowMissing(true))
		require.NoError(t, err)
		require.Nil(t, s.created)
		require.True(t, proto.Equal(&pb.Account{Name: "accounts/9", DisplayName: "new"}, out), "got %v", out)
		require.Equal(t, "client", req.GetUpdateTime(), "the request is not mutated")
	})
	t.Run("off saves", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		_, err := s.patch(t).Run(t.Context(), &pb.Account{DisplayName: "renamed"}, "", mask, update.WithValidateOnly(false))
		require.NoError(t, err)
		require.NotNil(t, s.saved)
	})

	refused := []struct {
		name  string
		store *accountStore
		req   *pb.Account
		tag   string
		mask  *fieldmaskpb.FieldMask
		opts  []update.PatchOption
		noCrt bool
		want  error
	}{
		{name: "stale etag", store: &accountStore{cur: storedAccount()}, req: &pb.Account{DisplayName: "x"},
			tag: etag.FromRevision[int64](2).String(), mask: mask, want: coreerrs.ErrConflict},
		{name: "malformed etag", store: &accountStore{cur: storedAccount()}, req: &pb.Account{DisplayName: "x"},
			tag: "garbage", mask: mask, want: coreerrs.ErrInvalidArgument},
		{name: "unknown mask path", store: &accountStore{cur: storedAccount()}, req: &pb.Account{DisplayName: "x"},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"nope"}}, want: fieldmask.ErrInvalidPath},
		{name: "changed immutable", store: &accountStore{cur: storedAccount()}, req: &pb.Account{Region: "us"},
			mask: &fieldmaskpb.FieldMask{Paths: []string{"region"}}, want: update.ErrProtectedField},
		{name: "empty required", store: &accountStore{cur: storedAccount()}, req: &pb.Account{},
			mask: mask, want: update.ErrBehaviorViolation},
		{name: "missing resource", store: &accountStore{}, req: &pb.Account{DisplayName: "x"}, mask: mask, want: coreerrs.ErrNotFound},
		{name: "etag for a missing resource", store: &accountStore{}, req: &pb.Account{DisplayName: "x"},
			tag: etag.FromRevision[int64](1).String(), mask: mask, opts: []update.PatchOption{update.WithAllowMissing(true)},
			want: coreerrs.ErrConflict},
		{name: "allow_missing without create", store: &accountStore{}, req: &pb.Account{DisplayName: "x"}, mask: mask,
			opts: []update.PatchOption{update.WithAllowMissing(true)}, noCrt: true, want: update.ErrInvalidPatch},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			patch := tt.store.patch(t)
			if tt.noCrt {
				patch.Create = nil
			}

			_, err := patch.Run(t.Context(), tt.req, tt.tag, tt.mask, append(tt.opts, validate)...)
			require.ErrorIs(t, err, tt.want)
			require.Nil(t, tt.store.saved)
			require.Nil(t, tt.store.created)
		})
	}
}

// TestTagPrecondition pins the opaque-etag preset: the etag is optional
// unless required, compared exactly by default, and a sent etag for a
// missing resource is a conflict.
func TestTagPrecondition(t *testing.T) {
	t.Parallel()

	current := func(a *pb.Account) coreetag.Tag { return coreetag.Strong(a.GetEtag()) }
	optional := update.TagPrecondition[*pb.Account]{Rule: etag.NewPrecondition(etag.WithField("account.etag")), Etag: current}
	stored := storedAccount() // etag "1"

	v, err := optional.Parse("")
	require.NoError(t, err)
	require.True(t, v.IsZero())
	require.NoError(t, optional.Check(stored, v))
	require.NoError(t, optional.Missing(v))

	v, err = optional.Parse(`"1"`)
	require.NoError(t, err)
	require.NoError(t, optional.Check(stored, v))
	require.ErrorIs(t, optional.Missing(v), coreerrs.ErrConflict)

	v, err = optional.Parse(`W/"1"`)
	require.NoError(t, err)
	require.ErrorIs(t, optional.Check(stored, v), etag.ErrMismatch, "exact comparison by default")

	weak := update.TagPrecondition[*pb.Account]{Rule: etag.NewPrecondition(etag.WithComparison(etag.CompareWeak)), Etag: current}
	require.NoError(t, weak.Check(stored, v))

	required := update.TagPrecondition[*pb.Account]{
		Rule: etag.NewPrecondition(etag.WithField("account.etag"), etag.WithRequired(true)), Etag: current,
	}
	_, err = required.Parse("")
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, "account.etag", fe.Field)
	require.Equal(t, etag.ReasonRequired, fe.Reason)

	var empty update.TagPrecondition[*pb.Account]

	_, err = empty.Parse("")
	require.ErrorIs(t, err, update.ErrInvalidPatch)
	require.ErrorIs(t, empty.Check(stored, coreetag.Tag{}), update.ErrInvalidPatch)
	require.ErrorIs(t, update.Int64Precondition[*pb.Account]{}.Check(stored, nil), update.ErrInvalidPatch)
}

// TestPatchRunTagPrecondition runs a Patch guarded by a content etag.
func TestPatchRunTagPrecondition(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	contentTag := func(a *pb.Account) coreetag.Tag {
		tag, hashErr := etag.FromMessage(a)
		require.NoError(t, hashErr)

		return tag
	}

	stored := storedAccount()

	var saved coreetag.Tag

	patch := update.Patch[*pb.Account, *pb.Account, coreetag.Tag]{
		Field: "account", Spec: spec,
		Precondition: update.TagPrecondition[*pb.Account]{Rule: etag.NewPrecondition(etag.WithField("account.etag")), Etag: contentTag},
		Read: func(context.Context) (*pb.Account, error) {
			return proto.Clone(stored).(*pb.Account), nil
		},
		ToProto: func(a *pb.Account) *pb.Account { return a },
		Save: func(_ context.Context, merged *pb.Account, _ update.Plan, expected coreetag.Tag) (*pb.Account, error) {
			saved = expected

			if stampErr := etag.Stamp(merged, contentTag(merged)); stampErr != nil {
				return nil, fmt.Errorf("stamp: %w", stampErr)
			}

			return merged, nil
		},
	}

	mask := &fieldmaskpb.FieldMask{Paths: []string{"display_name"}}
	cur := contentTag(stored)

	out, err := patch.Run(t.Context(), &pb.Account{DisplayName: "renamed"}, cur.String(), mask)
	require.NoError(t, err)
	require.Equal(t, cur, saved)
	require.Equal(t, contentTag(out).String(), out.GetEtag(), "the response carries the current etag")

	_, err = patch.Run(t.Context(), &pb.Account{DisplayName: "renamed"}, `"stale"`, mask)
	require.ErrorIs(t, err, coreerrs.ErrConflict)
}

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
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// accountStore is the stored side of an Account Patch test.
type accountStore struct {
	cur      *pb.Account // nil: the account does not exist
	readErr  error
	saved    *pb.Account
	created  *pb.Account
	expected *int64
}

func (s *accountStore) patch(t *testing.T) update.Patch[*pb.Account, *pb.Account, *int64] {
	t.Helper()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	return update.Patch[*pb.Account, *pb.Account, *int64]{
		Field: "account", Spec: spec,
		Precondition: update.Int64Precondition[*pb.Account]{Field: "account.etag", Version: func(*pb.Account) int64 { return 1 }},
		Read: func(context.Context) (*pb.Account, error) {
			switch {
			case s.readErr != nil:
				return nil, s.readErr
			case s.cur == nil:
				return nil, fmt.Errorf("account: %w", coreerrs.ErrNotFound)
			default:
				return proto.Clone(s.cur).(*pb.Account), nil
			}
		},
		ToProto: func(a *pb.Account) *pb.Account { return proto.Clone(a).(*pb.Account) },
		Save: func(_ context.Context, merged *pb.Account, _ update.Plan, expected *int64) (*pb.Account, error) {
			s.saved, s.expected = merged, expected
			return merged, nil
		},
		Create: func(_ context.Context, in *pb.Account) (*pb.Account, error) {
			s.created = in
			return in, nil
		},
	}
}

// TestPlanMaskPresence pins AIP-134: an omitted mask is the implied mask of
// the populated fields, an empty one is a no-op, whatever the request holds.
func TestPlanMaskPresence(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Resource{})
	require.NoError(t, err)

	req := &pb.Resource{Name: "n", Description: "d", Password: "secret", CreateTime: "client"}

	implied, err := spec.Plan(req, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"name", "description"}, implied.Paths)
	require.Equal(t, "secret", *implied.Secret("password", new("secret")))

	for name, opts := range map[string][]update.Option{"without stored": nil, "with stored": {update.WithCurrent(&pb.Resource{})}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			empty, err := spec.Plan(req, &fieldmaskpb.FieldMask{}, opts...)
			require.NoError(t, err)
			require.True(t, empty.Empty())
			require.Empty(t, empty.Paths)
			require.Nil(t, empty.Secret("password", new("secret")), "an empty mask changes no secret")
		})
	}

	_, err = spec.Plan(req, &fieldmaskpb.FieldMask{}, update.WithCurrent(&pb.Account{}))
	require.ErrorIs(t, err, update.ErrMessageMismatch)
}

// TestPatchRunMaskPresence pins that Patch keeps the presence of the mask:
// an empty mask saves nothing but still checks the precondition, an omitted
// one saves the populated fields.
func TestPatchRunMaskPresence(t *testing.T) {
	t.Parallel()

	req := &pb.Account{DisplayName: "renamed", Owner: &pb.Owner{Note: "n"}}

	t.Run("empty mask is a no-op", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		out, err := s.patch(t).Run(t.Context(), req, "", &fieldmaskpb.FieldMask{})
		require.NoError(t, err)
		require.Nil(t, s.saved)
		require.True(t, proto.Equal(storedAccount(), out), "got %v", out)
	})
	t.Run("empty mask still checks the etag", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		_, err := s.patch(t).Run(t.Context(), req, etag.FromRevision[int64](7).String(), &fieldmaskpb.FieldMask{})
		require.ErrorIs(t, err, coreerrs.ErrConflict)
		require.Nil(t, s.saved)
	})
	t.Run("omitted mask updates the populated fields", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		out, err := s.patch(t).Run(t.Context(), req, "", nil)
		require.NoError(t, err)

		want := storedAccount()
		want.DisplayName = "renamed"
		want.Owner.Note = "n"
		require.True(t, proto.Equal(want, out), "got %v", out)
		require.NotNil(t, s.saved)
	})
}

// TestInt64PreconditionOptionalEtag pins AIP-154: the etag is optional unless
// required, and a present stale one is a conflict.
func TestInt64PreconditionOptionalEtag(t *testing.T) {
	t.Parallel()

	version := func(*pb.Account) int64 { return 3 }
	optional := update.Int64Precondition[*pb.Account]{Field: "account.etag", Version: version}
	required := update.Int64Precondition[*pb.Account]{Field: "account.etag", Required: true, Version: version}

	v, err := optional.Parse("")
	require.NoError(t, err)
	require.Nil(t, v)
	require.NoError(t, optional.Check(&pb.Account{}, v))
	require.NoError(t, optional.Missing(v))

	_, err = required.Parse("")
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "err = %v", err)
	require.Equal(t, "account.etag", fe.Field)
	require.Equal(t, etag.ReasonRequired, fe.Reason)
	require.ErrorIs(t, err, etag.ErrMissing)

	for _, p := range []update.Int64Precondition[*pb.Account]{optional, required} {
		v, err = p.Parse(etag.FromRevision[int64](3).String())
		require.NoError(t, err)
		require.Equal(t, int64(3), *v)
		require.NoError(t, p.Check(&pb.Account{}, v))
		require.ErrorIs(t, p.Missing(v), coreerrs.ErrConflict)

		stale, err := p.Parse(etag.FromRevision[int64](2).String())
		require.NoError(t, err)
		require.ErrorIs(t, p.Check(&pb.Account{}, stale), coreerrs.ErrConflict)

		_, err = p.Parse("garbage")
		fe, ok = errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok)
		require.Equal(t, etag.ReasonInvalid, fe.Reason)
	}

	_, err = update.Int64Precondition[*pb.Account]{}.Parse("")
	require.ErrorIs(t, err, update.ErrInvalidPatch)

	s := &accountStore{cur: storedAccount()}
	_, err = s.patch(t).Run(t.Context(), &pb.Account{DisplayName: "x"}, "", nil)
	require.NoError(t, err)
	require.NotNil(t, s.saved)
	require.Nil(t, s.expected, "no etag, no version check")
}

// TestPatchRunAllowMissing pins AIP-134 allow_missing: a missing resource is
// created from the request (OUTPUT_ONLY cleared, mask ignored) only when the
// request opts in, Create is set and no etag is sent.
func TestPatchRunAllowMissing(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Name: "accounts/9", DisplayName: "new", UpdateTime: "client"}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"display_name"}}

	t.Run("creates a missing resource", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{}
		out, err := s.patch(t).Run(t.Context(), req, "", mask, update.WithAllowMissing(true))
		require.NoError(t, err)
		require.Nil(t, s.saved)
		require.True(t, proto.Equal(&pb.Account{Name: "accounts/9", DisplayName: "new"}, s.created), "got %v", s.created)
		require.True(t, proto.Equal(s.created, out))
		require.Equal(t, "client", req.GetUpdateTime(), "the request is not mutated")
	})
	t.Run("updates an existing resource", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		_, err := s.patch(t).Run(t.Context(), &pb.Account{DisplayName: "renamed"}, "", mask, update.WithAllowMissing(true))
		require.NoError(t, err)
		require.Nil(t, s.created)
		require.Equal(t, "renamed", s.saved.GetDisplayName())
	})

	refused := []struct {
		name  string
		store *accountStore
		tag   string
		opts  []update.PatchOption
		noCrt bool
		want  error
	}{
		{name: "not requested", store: &accountStore{}, want: coreerrs.ErrNotFound},
		{name: "explicitly off", store: &accountStore{}, opts: []update.PatchOption{update.WithAllowMissing(false)}, want: coreerrs.ErrNotFound},
		{name: "etag for a missing resource", store: &accountStore{}, tag: etag.FromRevision[int64](1).String(),
			opts: []update.PatchOption{update.WithAllowMissing(true)}, want: coreerrs.ErrConflict},
		{name: "other read errors propagate", store: &accountStore{readErr: coreerrs.ErrUnavailable},
			opts: []update.PatchOption{update.WithAllowMissing(true)}, want: coreerrs.ErrUnavailable},
		{name: "no create hook", store: &accountStore{}, noCrt: true,
			opts: []update.PatchOption{update.WithAllowMissing(true)}, want: update.ErrInvalidPatch},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			patch := tt.store.patch(t)
			if tt.noCrt {
				patch.Create = nil
			}

			_, err := patch.Run(t.Context(), req, tt.tag, mask, tt.opts...)
			require.ErrorIs(t, err, tt.want)
			require.Nil(t, tt.store.created)
		})
	}

	t.Run("an invalid mask is refused before the create", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{}
		_, err := s.patch(t).Run(t.Context(), req, "", &fieldmaskpb.FieldMask{Paths: []string{"nope"}}, update.WithAllowMissing(true))
		require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
		require.Nil(t, s.created)
	})
}

// TestPlanErrorLocation pins AIP-193 locations: a refusal names the offending
// mask path, "updateMask.paths[i]" under the configured field name, and
// carries a *MaskPathError; the implied and "*" masks have no path.
func TestPlanErrorLocation(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{}, update.WithUpdateMaskFieldName("update_mask"))
	require.NoError(t, err)

	current := update.WithCurrent(storedAccount())

	tests := []struct {
		name   string
		mask   *fieldmaskpb.FieldMask
		mutate func(*pb.Account)
		field  string
		index  int
		path   string
		cause  error
	}{
		{name: "unknown path", mask: &fieldmaskpb.FieldMask{Paths: []string{"display_name", "nope"}},
			field: "update_mask.paths[1]", index: 1, path: "nope", cause: fieldmask.ErrInvalidPath},
		{name: "wildcard not alone", mask: &fieldmaskpb.FieldMask{Paths: []string{"display_name", "*"}},
			field: "update_mask.paths[1]", index: 1, path: "*", cause: fieldmask.ErrWildcardNotAlone},
		{name: "malformed path", mask: &fieldmaskpb.FieldMask{Paths: []string{"owner."}},
			field: "update_mask.paths[0]", index: 0, path: "owner.", cause: fieldmask.ErrInvalidPath},
		{name: "nested immutable", mask: &fieldmaskpb.FieldMask{Paths: []string{"display_name", "owner.note", "owner.org"}},
			mutate: func(a *pb.Account) { a.Owner.Org = "x" },
			field:  "update_mask.paths[2]", index: 2, path: "owner.org", cause: update.ErrProtectedField},
		{name: "parent covering a changed immutable", mask: &fieldmaskpb.FieldMask{Paths: []string{"display_name", "owner"}},
			mutate: func(a *pb.Account) { a.Owner.Org = "x" },
			field:  "update_mask.paths[1]", index: 1, path: "owner", cause: update.ErrProtectedField},
		{name: "implied mask", mutate: func(a *pb.Account) { a.Region = "x" },
			field: "update_mask", index: -1, cause: update.ErrProtectedField},
		{name: "full replacement", mask: &fieldmaskpb.FieldMask{Paths: []string{"*"}}, mutate: func(a *pb.Account) { a.DisplayName = "" },
			field: "update_mask", index: -1, cause: update.ErrBehaviorViolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedAccount()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			_, err := spec.Plan(req, tt.mask, current)
			require.ErrorIs(t, err, tt.cause)
			require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

			fe, mp := maskPathError(t, err)
			require.Equal(t, tt.field, fe.Field)
			require.Equal(t, tt.index, mp.Index)
			require.Equal(t, tt.path, mp.Path)
			require.NotEmpty(t, mp.Error())
		})
	}

	_, err = update.SelectsAll([]string{"a", "*"})
	require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone)
	fe, mp := maskPathError(t, err)
	require.Equal(t, update.DefaultUpdateMaskField+".paths[1]", fe.Field)
	require.Equal(t, 1, mp.Index)
}

// TestPatchRunAIP203Regression pins the AIP-203 behaviors Patch keeps:
// OUTPUT_ONLY values are ignored, an unchanged IMMUTABLE or IDENTIFIER value
// is ignored and a changed one refused on the resource field, and an empty
// REQUIRED field is refused on the mask path.
func TestPatchRunAIP203Regression(t *testing.T) {
	t.Parallel()

	t.Run("output only and unchanged protected values", func(t *testing.T) {
		t.Parallel()

		s := &accountStore{cur: storedAccount()}
		req := storedAccount()
		req.DisplayName, req.UpdateTime, req.Uid = "renamed", "client", "client"
		out, err := s.patch(t).Run(t.Context(), req, "",
			&fieldmaskpb.FieldMask{Paths: []string{"display_name", "update_time", "uid", "region", "name"}})
		require.NoError(t, err)

		want := storedAccount()
		want.DisplayName = "renamed"
		require.True(t, proto.Equal(want, out), "got %v", out)
	})

	refused := []struct {
		name   string
		paths  []string
		mutate func(*pb.Account)
		field  string
		cause  error
	}{
		{name: "changed immutable", paths: []string{"region"}, mutate: func(a *pb.Account) { a.Region = "us" },
			field: "account.region", cause: update.ErrProtectedField},
		{name: "changed identifier", paths: []string{"display_name"}, mutate: func(a *pb.Account) { a.Name = "accounts/2" },
			field: "account.name", cause: update.ErrProtectedField},
		{name: "empty required", paths: []string{"display_name"}, mutate: func(a *pb.Account) { a.DisplayName = "" },
			field: update.DefaultUpdateMaskField + ".paths[0]", cause: update.ErrBehaviorViolation},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &accountStore{cur: storedAccount()}
			req := storedAccount()
			tt.mutate(req)

			_, err := s.patch(t).Run(t.Context(), req, "", &fieldmaskpb.FieldMask{Paths: tt.paths})
			require.ErrorIs(t, err, tt.cause)

			fe, ok := errors.AsType[*coreerrs.FieldError](err)
			require.True(t, ok, "err = %v", err)
			require.Equal(t, tt.field, fe.Field)
			require.Nil(t, s.saved)
		})
	}
}

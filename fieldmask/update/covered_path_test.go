// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	sdpb "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
)

// coveredResource is a request resource whose profile holds an IDENTIFIER.
func coveredResource() *pb.Resource {
	return &pb.Resource{
		Description: "d",
		Profile:     &pb.Profile{Id: "p", DisplayName: "n"},
		Aliases:     []*pb.Profile{{Id: "a", DisplayName: "x"}},
	}
}

// TestPlan_CoveredPathChecked pins that Spec.Plan checks every raw path on
// its own before the mask collapses the paths another one covers: a covered
// path naming an IDENTIFIER, an unknown path and an index are refused on the
// covered path, whichever order the paths come in.
func TestPlan_CoveredPathChecked(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Resource{})
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		paths   []string
		index   int
		cause   error
		current *pb.Resource
	}{
		{name: "identifier under its message", paths: []string{"profile", "profile.id"}, index: 1, cause: update.ErrProtectedField, current: &pb.Resource{Profile: &pb.Profile{Id: "other"}}},
		{name: "identifier first", paths: []string{"profile.id", "profile"}, index: 0, cause: update.ErrProtectedField, current: &pb.Resource{Profile: &pb.Profile{Id: "other"}}},
		{name: "changed identifier", paths: []string{"profile", "profile.id"}, index: 1, cause: update.ErrProtectedField,
			current: &pb.Resource{Profile: &pb.Profile{Id: "other"}}},
		{name: "unknown path under a scalar", paths: []string{"description", "description.no_such"}, index: 1, cause: fieldmask.ErrInvalidPath},
		{name: "index under its list", paths: []string{"aliases", "aliases.0.id"}, index: 1, cause: fieldmask.ErrInvalidPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var opts []update.Option
			if tc.current != nil {
				opts = append(opts, update.WithCurrent(tc.current))
			}

			_, err := spec.Plan(coveredResource(), &fieldmaskpb.FieldMask{Paths: tc.paths}, opts...)
			require.ErrorIs(t, err, tc.cause)

			mp, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, tc.index, mp.Index)
			require.Equal(t, tc.paths[tc.index], mp.Path)
		})
	}

	// Covered by an unchanged parent, an unchanged identifier is ignored.
	current := coveredResource()
	plan, err := spec.Plan(coveredResource(), &fieldmaskpb.FieldMask{Paths: []string{"profile", "profile.id"}}, update.WithCurrent(current))
	require.NoError(t, err)
	require.Equal(t, []string{"profile.display_name", "profile.secret"}, plan.Paths)
}

// TestApply_CoveredPathChecked pins that Apply and ValidateStrict check a
// path that another path of the mask covers once both are resolved — the
// parent spelled by its JSON name ("backupOwner") and the child by its
// declared one — as if sent alone: naming the IMMUTABLE org while leaving it
// unset changes it, though replacing backup_owner whole keeps it.
func TestApply_CoveredPathChecked(t *testing.T) {
	t.Parallel()

	req := &pb.Account{BackupOwner: &pb.Owner{Email: "e"}}
	current := &pb.Account{BackupOwner: &pb.Owner{Email: "e", Org: "o"}}
	want := []FieldViolation{{Path: "backup_owner.org", Behavior: annotations.FieldBehavior_IMMUTABLE,
		Reason: "immutable field cannot be changed from its stored value"}}

	t.Run("covered child", func(t *testing.T) {
		t.Parallel()

		msg := proto.Clone(req)
		err := update.Apply(fieldmask.FromPaths("backupOwner", "backup_owner.org"), msg, update.WithCurrent(current))

		be, ok := errors.AsType[*update.BehaviorError](err)
		require.True(t, ok, "%v", err)
		require.Equal(t, want, be.Violations)
		require.True(t, proto.Equal(req, msg), "msg left untouched")

		_, err = update.ValidateStrict(fieldmask.FromPaths("backupOwner", "backup_owner.org"), req, update.WithCurrent(current))

		se, ok := errors.AsType[*update.StrictError](err)
		require.True(t, ok, "%v", err)
		require.Equal(t, want, se.Violations)
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})

	t.Run("parent alone", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, update.Apply(fieldmask.FromPaths("backupOwner"), proto.Clone(req), update.WithCurrent(current)), update.ErrBehaviorViolation)

		_, err := update.ValidateStrict(fieldmask.FromPaths("backupOwner"), req, update.WithCurrent(current))
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})

	t.Run("covered atomic sub-path", func(t *testing.T) {
		t.Parallel()

		// A covered sub-path into an atomic value is refused as if sent alone.
		draft := &sdpb.Draft{DeleteTime: timestamppb.New(time.Unix(1, 0))}
		err := update.Apply(fieldmask.FromPaths("deleteTime", "delete_time.seconds"), proto.Clone(draft))
		requireValidationPath(t, err, "delete_time")

		_, err = update.ValidateStrict(fieldmask.FromPaths("deleteTime", "delete_time.seconds"), draft)
		requireValidationPath(t, err, "delete_time")

		require.NoError(t, update.Apply(fieldmask.FromPaths("deleteTime", "delete_time"), proto.Clone(draft)))
	})
}

// requireValidationPath asserts that err holds a *fieldmask.ValidationError
// on path.
func requireValidationPath(t *testing.T, err error, path string) {
	t.Helper()

	ve, ok := errors.AsType[*fieldmask.ValidationError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, path, ve.Path)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
}

// FieldViolation shortens the violation type in the tables.
type FieldViolation = update.FieldViolation

// TestCoveredPath_ErrorMessages pins that a mask-level violation, which names
// no path, renders no empty path or leading separator.
func TestCoveredPath_ErrorMessages(t *testing.T) {
	t.Parallel()

	require.Equal(t, "field behavior violation: r", (&update.BehaviorError{Violations: []FieldViolation{{Reason: "r"}}}).Error())
	require.Equal(t, "field behavior violation: a: r; r", (&update.BehaviorError{Violations: []FieldViolation{{Path: "a", Reason: "r"}, {Reason: "r"}}}).Error())
	require.Equal(t, "invalid update_mask: r", (&update.StrictError{Violations: []FieldViolation{{Reason: "r"}}}).Error())
}

// TestApply_CollapsedParentEnforcesSubtree pins that a mask FromProtoFieldMask
// collapsed (["profile", "profile.id"] holds profile alone) stays safe under
// Apply and ValidateStrict: the covering parent is replaced through its other
// fields, so the IDENTIFIER keeps its stored value or the request's address,
// a changed one is refused with current state, unverified protected values
// are retained, and OUTPUT_ONLY values are cleared.
func TestApply_CollapsedParentEnforcesSubtree(t *testing.T) {
	t.Parallel()

	collapsed := func(t *testing.T, paths ...string) fieldmask.FieldMask {
		t.Helper()

		mask, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: paths})
		require.NoError(t, err)

		return mask
	}

	t.Run("identifier kept as the address", func(t *testing.T) {
		t.Parallel()

		msg := &pb.Resource{Profile: &pb.Profile{Id: "p", DisplayName: "n", UpdatedAt: "u"}}
		mask := collapsed(t, "profile", "profile.id")
		require.Equal(t, []string{"profile"}, mask.ToPaths())

		require.NoError(t, update.Apply(mask, msg))
		update.DropIdentifiers(mask, msg)
		require.Equal(t, []string{"profile.display_name", "profile.secret"}, mask.ToPaths(), "no write-back path covers profile.id")
		requireProtoEqual(t, &pb.Resource{Profile: &pb.Profile{Id: "p", DisplayName: "n"}}, msg)
	})

	t.Run("changed identifier refused", func(t *testing.T) {
		t.Parallel()

		msg := &pb.Resource{Profile: &pb.Profile{Id: "p", DisplayName: "n"}}
		current := &pb.Resource{Profile: &pb.Profile{Id: "other"}}
		want := []FieldViolation{{Path: "profile.id", Behavior: annotations.FieldBehavior_IDENTIFIER,
			Reason: "identifier field cannot be changed from its stored value"}}

		err := update.Apply(collapsed(t, "profile.id", "profile"), msg, update.WithCurrent(current))
		require.Equal(t, want, behaviorViolations(t, err))

		_, err = update.ValidateStrict(collapsed(t, "profile.id", "profile"), msg, update.WithCurrent(current))
		se, ok := errors.AsType[*update.StrictError](err)
		require.True(t, ok, "%v", err)
		require.Equal(t, want, se.Violations)
	})

	t.Run("immutable without the stored resource retained", func(t *testing.T) {
		t.Parallel()

		msg := &pb.Account{BackupOwner: &pb.Owner{Email: "e", Org: "o"}}
		err := update.Apply(collapsed(t, "backup_owner", "backup_owner.org"), msg)
		require.NoError(t, err)
		require.Equal(t, "o", msg.GetBackupOwner().GetOrg())
	})
}

// TestPlan_CoveredPathCountsAndOneofs pins that Spec.Plan runs the element
// count and oneof checks on a covered path as if sent alone: with two stored
// aliases and one in the request, ["aliases", "aliases.*.display_name"] is
// refused as ["aliases.*.display_name"] is; with a stored IMMUTABLE pinned,
// ["items.k", "items.k.free"] is refused as ["items.k.free"] is. ApplyMask
// refuses the second the same way.
func TestPlan_CoveredPathCountsAndOneofs(t *testing.T) {
	t.Parallel()

	t.Run("element count", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(&pb.Resource{})
		require.NoError(t, err)

		req := &pb.Resource{Aliases: []*pb.Profile{{DisplayName: "a"}}}
		current := update.WithCurrent(&pb.Resource{Aliases: []*pb.Profile{{DisplayName: "x"}, {DisplayName: "y"}}})

		for _, paths := range [][]string{{"aliases.*.display_name"}, {"aliases", "aliases.*.display_name"}, {"aliases.*.display_name", "aliases"}} {
			_, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, current)
			require.ErrorIs(t, err, update.ErrElementCountMismatch, "%v", paths)
		}

		plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"aliases"}}, current)
		require.NoError(t, err)
		require.Equal(t, []string{"aliases"}, plan.Paths)
	})

	t.Run("oneof", func(t *testing.T) {
		t.Parallel()

		md := collectionDescriptor(t)
		stored := collectionRoot(md, map[string]map[string]string{"k": {"pinned": "old"}}, nil, nil)
		req := collectionRoot(md, map[string]map[string]string{"k": {"free": "new"}}, nil, nil)

		spec, err := update.NewSpec(dynamicpb.NewMessage(md))
		require.NoError(t, err)

		for _, paths := range [][]string{{"items.k.free"}, {"items.k", "items.k.free"}} {
			_, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
			require.ErrorIs(t, err, update.ErrProtectedField, "%v", paths)

			mp, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok)
			require.Equal(t, 0, mp.Index)

			_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), update.WithCurrent(stored))
			require.ErrorIs(t, err, update.ErrBehaviorViolation, "%v", paths)
		}

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"items.k"}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrProtectedField)
	})
}

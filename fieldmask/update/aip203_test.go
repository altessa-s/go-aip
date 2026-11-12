// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// storedAccount returns an Account whose every REQUIRED field is set, as a
// service would store it.
func storedAccount() *pb.Account {
	return &pb.Account{
		Name:        "accounts/1",
		DisplayName: "Acme",
		Key:         []byte{1},
		Enabled:     true,
		State:       pb.State_ACTIVE,
		Quota:       new(int32(5)),
		Owner:       &pb.Owner{Email: "o@example.com", Org: "org-1", CreateTime: "server"},
		Emails:      []string{"a@example.com"},
		Attributes:  map[string]string{"k": "v"},
		Members:     []*pb.Owner{{Email: "m@example.com", Org: "m-org"}},
		Delegates:   map[string]*pb.Owner{"k": {Email: "d@example.com", Org: "d-org"}},
		Billing:     &pb.Account_Card{Card: "4242"},
		Region:      "eu",
		UpdateTime:  "server",
		Etag:        "1",
	}
}

// behaviorViolations returns the violations of an *UpdateMaskBehaviorError.
func behaviorViolations(t *testing.T, err error) []update.FieldViolation {
	t.Helper()

	behaviorErr, ok := errors.AsType[*update.BehaviorError](err)
	require.True(t, ok, "want *UpdateMaskBehaviorError, got %v", err)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)

	return behaviorErr.Violations
}

// TestApplyUpdateMask_AIP203 pins every AIP-203 rule of Apply, with
// and without the stored resource.
func TestApplyUpdateMask_AIP203(t *testing.T) {
	t.Parallel()

	type violation struct {
		path     string
		behavior annotations.FieldBehavior
		reason   string
	}

	tests := []struct {
		name      string
		paths     []string
		mutate    func(*pb.Account)
		current   bool
		wantPaths []string
		want      []violation
	}{
		{
			name: "output-only paths are ignored at any depth", paths: []string{"update_time", "owner.create_time", "uid", "display_name"},
			wantPaths: []string{"display_name"},
		},
		{
			name: "immutable path without the stored resource", paths: []string{"region"},
			wantPaths: []string{},
		},
		{name: "unchanged immutable path", paths: []string{"region", "display_name"}, current: true, wantPaths: []string{"display_name"}},
		{
			name: "changed immutable path", paths: []string{"region"}, current: true, mutate: func(a *pb.Account) { a.Region = "us" },
			want: []violation{{"region", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{
			name: "cleared immutable path", paths: []string{"owner.org"}, current: true, mutate: func(a *pb.Account) { a.Owner.Org = "" },
			want: []violation{{"owner.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{
			name: "identifier path without the stored resource", paths: []string{"name"},
			wantPaths: []string{},
		},
		{name: "unchanged identifier path updates nothing", paths: []string{"name"}, current: true, wantPaths: []string{}},
		{
			name: "changed identifier the mask does not name", paths: []string{"display_name"}, current: true,
			mutate: func(a *pb.Account) { a.Name = "accounts/2" },
			want:   []violation{{"name", annotations.FieldBehavior_IDENTIFIER, "identifier field cannot be changed from its stored value"}},
		},
		{
			name: "identifier the mask does not name, without the stored resource", paths: []string{"display_name"},
			mutate: func(a *pb.Account) { a.Name = "accounts/2" }, wantPaths: []string{"display_name"},
		},
		{
			name: "required path set to an empty value", paths: []string{"display_name", "quota", "state"},
			mutate: func(a *pb.Account) { a.DisplayName, a.Quota, a.State = "", new(int32(0)), pb.State_STATE_UNSPECIFIED },
			want: []violation{
				{"display_name", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"},
				{"quota", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"},
				{"state", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"},
			},
		},
		{
			name: "required field absent from the mask may be omitted", paths: []string{"etag", "invoice_email"}, current: true,
			mutate: func(a *pb.Account) { a.DisplayName, a.Owner = "", nil }, wantPaths: []string{"invoice_email"},
		},
		{
			name: "nested required path", paths: []string{"owner.email"}, mutate: func(a *pb.Account) { a.Owner.Email = "" },
			want: []violation{{"owner.email", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"}},
		},
		{
			name: "replaced message left without its required field", paths: []string{"backup_owner"},
			mutate: func(a *pb.Account) { a.BackupOwner = &pb.Owner{Note: "n"} },
			want:   []violation{{"backup_owner.email", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"}},
		},
		{
			name: "cleared optional message", paths: []string{"backup_owner"}, mutate: func(a *pb.Account) { a.BackupOwner = nil },
			current: true, wantPaths: []string{"backup_owner.email", "backup_owner.note"},
		},
		{
			name: "cleared required message", paths: []string{"owner"}, mutate: func(a *pb.Account) { a.Owner = nil },
			want: []violation{{"owner", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"}},
		},
		{
			name: "replaced collection element without its required field", paths: []string{"members"},
			mutate: func(a *pb.Account) { a.Members = append(a.Members, &pb.Owner{Note: "n"}) },
			want:   []violation{{"members.1.email", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"}},
		},
		{
			name: "replaced message keeps an unchanged immutable value", paths: []string{"owner"}, current: true,
			mutate: func(a *pb.Account) { a.Owner.Note = "new" }, wantPaths: []string{"owner.email", "owner.note"},
		},
		{
			name: "replaced message with a changed immutable value", paths: []string{"owner"}, current: true,
			mutate: func(a *pb.Account) { a.Owner.Org = "other" },
			want:   []violation{{"owner.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{
			name: "replaced message, immutable left unset", paths: []string{"owner"}, mutate: func(a *pb.Account) { a.Owner.Org = "" },
			wantPaths: []string{"owner.email", "owner.note"},
		},
		{
			name: "replaced collection elements are compared", paths: []string{"members", "delegates"}, current: true,
			mutate: func(a *pb.Account) {
				a.Members = []*pb.Owner{{Email: "x@example.com", Org: "new"}}
				a.Delegates = map[string]*pb.Owner{"k": {Email: "x@example.com", Org: "new"}}
			},
			want: []violation{{"members.0.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}, {"delegates.k.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{
			name: "map entry sub-path compared by key", paths: []string{"delegates.k.org"}, current: true,
			mutate: func(a *pb.Account) { a.Delegates["k"].Org = "other" },
			want:   []violation{{"delegates.k.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{name: "unchanged map entry sub-path", paths: []string{"delegates.k.org", "delegates.k.note"}, current: true,
			wantPaths: []string{"delegates.k.note"}},
		{
			name: "list element sub-path compared by index", paths: []string{"members.*.org"}, current: true,
			mutate: func(a *pb.Account) { a.Members[0].Org = "other" },
			want:   []violation{{"members.0.org", annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"}},
		},
		{
			name: "element sub-path to a required field", paths: []string{"members.*.email", "delegates.k.email"},
			mutate: func(a *pb.Account) { a.Members[0].Email, a.Delegates["k"].Email = "", "" },
			want: []violation{
				{"delegates.k.email", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"},
				{"members.0.email", annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"},
			},
		},
		{
			name: "message holding only output-only fields, recursively", paths: []string{"settings"},
			mutate:    func(a *pb.Account) { a.Settings = &pb.Settings{Theme: "dark", UpdatedBy: "client"} },
			wantPaths: []string{"settings.theme"},
		},
		{
			name: "recursive message held by the stored resource", paths: []string{"settings"}, current: true,
			mutate:    func(a *pb.Account) { a.Settings = &pb.Settings{Theme: "dark", Defaults: &pb.Settings{Theme: "light"}} },
			wantPaths: []string{"settings.defaults.theme", "settings.theme"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedAccount()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			var opts []update.Option
			if tt.current {
				opts = append(opts, update.WithCurrent(storedAccount()))
			}

			before := proto.Clone(req)
			mask := fieldmask.FromPaths(tt.paths...)
			err := update.Apply(mask, req, opts...)

			if tt.want != nil {
				violations := behaviorViolations(t, err)
				got := make([]violation, 0, len(violations))

				for _, v := range violations {
					got = append(got, violation{v.Path, v.Behavior, v.Reason})
				}

				require.Equal(t, tt.want, got)
				require.True(t, proto.Equal(before, req), "a rejected update leaves the message untouched")
				require.Equal(t, fieldmask.FromPaths(tt.paths...), mask, "a rejected update leaves the mask untouched")

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantPaths, mask.ToPaths())

			if mask.IsEmpty() {
				require.Empty(t, req.GetUpdateTime(), "output-only input is cleared even when no path remains")
				return
			}

			assert.Empty(t, req.GetUpdateTime(), "OUTPUT_ONLY values are cleared from the request")
		})
	}
}

// TestApplyUpdateMask_ClearsOutputOnlyInCollections pins that the
// OUTPUT_ONLY values inside a collection replaced whole are cleared from the
// request (AIP-203), not reported.
func TestApplyUpdateMask_ClearsOutputOnlyInCollections(t *testing.T) {
	t.Parallel()

	req := storedAccount()
	req.Members = []*pb.Owner{{Email: "m@example.com", CreateTime: "client"}}
	req.Delegates = map[string]*pb.Owner{"k": {Email: "d@example.com", CreateTime: "client"}}

	mask := fieldmask.FromPaths("members", "delegates")
	require.NoError(t, update.Apply(mask, req))
	assert.Empty(t, req.GetMembers()[0].GetCreateTime())
	assert.Empty(t, req.GetDelegates()["k"].GetCreateTime())
	assert.Equal(t, "m@example.com", req.GetMembers()[0].GetEmail())
}

// TestApplyUpdateMask_FullReplacement pins that "*" alone replaces the whole
// resource: every field but the OUTPUT_ONLY ones, IMMUTABLE and IDENTIFIER
// values compared with the stored ones, REQUIRED fields checked.
func TestApplyUpdateMask_FullReplacement(t *testing.T) {
	t.Parallel()

	req := storedAccount()
	mask := fieldmask.FromPaths("*")
	require.NoError(t, update.Apply(mask, req, update.WithCurrent(storedAccount())))
	// Sub-messages holding server-managed fields that neither side holds
	// (backup_owner, settings, parent) and the unset oneof member are left
	// out; region is unchanged and name is kept as the address.
	require.Equal(t, []string{"attributes", "card", "delegates", "display_name", "emails", "enabled", "key", "members", "owner.email", "owner.note", "quota", "state"}, mask.ToPaths())
	assert.Empty(t, req.GetRegion(), "the unchanged IMMUTABLE value is ignored")

	req = storedAccount()
	req.DisplayName = ""
	violations := behaviorViolations(t, update.Apply(fieldmask.FromPaths("*"), req, update.WithCurrent(storedAccount())))
	require.Equal(t, "display_name", violations[0].Path)

	req = storedAccount()
	req.Region = "us"
	violations = behaviorViolations(t, update.Apply(fieldmask.FromPaths("*"), req, update.WithCurrent(storedAccount())))
	require.Equal(t, "region", violations[0].Path)

	req = storedAccount()
	mask = fieldmask.FromPaths("*")
	require.NoError(t, update.Apply(mask, req))
	require.NotContains(t, mask, "region")
	require.Equal(t, "eu", req.GetRegion())

	var ve *fieldmask.ValidationError
	require.ErrorAs(t, update.Apply(fieldmask.FromPaths("*", "name"), storedAccount()), &ve)
}

// TestApplyUpdateMask_CurrentMismatch pins that a stored resource of another
// type is refused before anything is applied.
func TestApplyUpdateMask_CurrentMismatch(t *testing.T) {
	t.Parallel()

	req := storedAccount()
	err := update.Apply(fieldmask.FromPaths("display_name"), req, update.WithCurrent(&pb.Owner{}))
	require.ErrorIs(t, err, update.ErrMessageMismatch)
	require.True(t, proto.Equal(storedAccount(), req))

	_, err = update.ValidateStrict(fieldmask.FromPaths("display_name"), req, update.WithCurrent(&pb.Owner{}))
	require.ErrorIs(t, err, update.ErrMessageMismatch)

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)
	_, err = spec.Plan(req, nil, update.WithCurrent(&pb.Owner{}))
	require.ErrorIs(t, err, update.ErrMessageMismatch)
}

// TestValidateStrictUpdateMask_AIP203 pins the AIP-203 rules of the strict
// mask validation: OUTPUT_ONLY dropped, IMMUTABLE and IDENTIFIER compared,
// REQUIRED checked, "*" a full replacement.
func TestValidateStrictUpdateMask_AIP203(t *testing.T) {
	t.Parallel()

	current := update.WithCurrent(storedAccount())

	mask, err := update.ValidateStrict(fieldmask.FromPaths("update_time", "region", "owner", "display_name"), storedAccount(), current)
	require.NoError(t, err)
	require.Equal(t, []string{"display_name", "owner.email", "owner.note"}, mask.ToPaths())

	req := storedAccount()
	req.Region, req.DisplayName = "us", ""
	_, err = update.ValidateStrict(fieldmask.FromPaths("region", "display_name"), req, current)

	var strict *update.StrictError
	require.ErrorAs(t, err, &strict)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
	require.Equal(t, []string{"display_name", "region"}, []string{strict.Violations[0].Path, strict.Violations[1].Path})

	_, err = update.ValidateStrict(fieldmask.FromPaths("region"), storedAccount())
	require.NoError(t, err, "without current state the comparison is deferred")

	mask, err = update.ValidateStrict(fieldmask.FromPaths("*"), storedAccount(), current)
	require.NoError(t, err)
	require.NotContains(t, mask.ToPaths(), "update_time")
	require.NotContains(t, mask.ToPaths(), "region")

	req = storedAccount()
	req.Owner = nil
	_, err = update.ValidateStrict(fieldmask.FromPaths("*"), req, current)
	require.ErrorAs(t, err, &strict)
	require.Equal(t, "owner", strict.Violations[0].Path)
}

// TestPlan_AIP203 pins the AIP-203 rules of MaskSpec.Plan on a generated
// resource whose etag carries no field behavior (AIP-154).
func TestPlan_AIP203(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	current := update.WithCurrent(storedAccount())

	plan, err := spec.Plan(storedAccount(), maskOf([]string{"update_time", "region", "display_name", "owner"}), current)
	require.NoError(t, err)
	require.Equal(t, []string{"display_name", "owner.email", "owner.note"}, plan.Paths)

	plan, err = spec.Plan(storedAccount(), nil, current)
	require.NoError(t, err)
	require.NotContains(t, plan.Paths, "region")

	refused := []struct {
		name   string
		paths  []string
		mutate func(*pb.Account)
		opts   []update.Option
		reason string
		cause  error
		field  string
	}{
		{name: "changed immutable", paths: []string{"region"}, mutate: func(a *pb.Account) { a.Region = "us" }, opts: []update.Option{current},
			reason: `path "region" cannot be changed`, cause: update.ErrProtectedField, field: "updateMask.paths[0]"},
		{name: "changed identifier", paths: []string{"display_name"}, mutate: func(a *pb.Account) { a.Name = "accounts/2" },
			opts: []update.Option{current}, reason: `path "name" cannot be changed`, cause: update.ErrProtectedField, field: "updateMask"},
		{name: "empty required", paths: []string{"display_name"}, mutate: func(a *pb.Account) { a.DisplayName = "" },
			reason: `path "display_name" is required and cannot be empty`, cause: update.ErrBehaviorViolation, field: "updateMask.paths[0]"},
		{name: "full replacement without a required field", paths: []string{"*"}, mutate: func(a *pb.Account) { a.Emails = nil },
			opts: []update.Option{current}, reason: `path "emails" is required and cannot be empty`, cause: update.ErrBehaviorViolation, field: "updateMask"},
		{name: "etag is not updatable", paths: []string{"etag"}, reason: `path "etag" cannot be updated`, cause: update.ErrProtectedField, field: "updateMask.paths[0]"},
		{name: "implied changed immutable", mutate: func(a *pb.Account) { a.Owner.Org = "other" }, opts: []update.Option{current},
			reason: `path "owner.org" cannot be changed`, cause: update.ErrProtectedField, field: "updateMask"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedAccount()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			_, err := spec.Plan(req, maskOf(tt.paths), tt.opts...)

			var fe *coreerrs.FieldError
			require.ErrorAs(t, err, &fe)

			require.Equal(t, tt.field, fe.Field)
			require.Equal(t, tt.reason, fe.Reason)
			require.ErrorIs(t, err, tt.cause)
		})
	}
}

// TestPatchRun_AIP203 pins that Patch plans against the stored resource: an
// unchanged immutable value is ignored, a changed one is reported on the
// resource field, and client OUTPUT_ONLY values never reach the store.
func TestPatchRun_AIP203(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	var saved *pb.Account

	patch := update.Patch[*pb.Account, *pb.Account, *int64]{
		Field:        "account",
		Spec:         spec,
		Precondition: update.Int64Precondition[*pb.Account]{Field: "account.etag", Version: func(*pb.Account) int64 { return 1 }},
		Read:         func(context.Context) (*pb.Account, error) { return storedAccount(), nil },
		ToProto:      func(a *pb.Account) *pb.Account { return proto.Clone(a).(*pb.Account) },
		Save: func(_ context.Context, merged *pb.Account, _ update.Plan, _ *int64) (*pb.Account, error) {
			saved = merged
			return merged, nil
		},
	}
	tag := etag.FromRevision[int64](1).String()

	req := storedAccount()
	req.DisplayName, req.UpdateTime = "New", "client"
	req.Members = []*pb.Owner{{Email: "n@example.com", Org: "m-org", CreateTime: "client"}}
	out, err := patch.Run(t.Context(), req, tag, &fieldmaskpb.FieldMask{Paths: []string{"display_name", "region", "update_time", "members"}})
	require.NoError(t, err)
	require.Equal(t, "New", out.GetDisplayName())
	require.Equal(t, "server", saved.GetUpdateTime())
	require.Empty(t, saved.GetMembers()[0].GetCreateTime(), "client OUTPUT_ONLY values never reach the store")

	req = storedAccount()
	req.Region = "us"
	_, err = patch.Run(t.Context(), req, tag, &fieldmaskpb.FieldMask{Paths: []string{"region"}})

	var fe *coreerrs.FieldError
	require.ErrorAs(t, err, &fe)
	require.Equal(t, "account.region", fe.Field)
	require.Equal(t, "cannot be changed", fe.Reason)
	require.ErrorIs(t, err, update.ErrProtectedField)

	_, err = (update.Patch[*pb.Account, *pb.Account, *int64]{Precondition: patch.Precondition, Read: patch.Read, ToProto: patch.ToProto,
		Save: patch.Save}).Run(t.Context(), req, tag, nil)
	require.ErrorIs(t, err, update.ErrInvalidPatch, "a patch without a spec is invalid")
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// Violation reasons of the AIP-203 checks.
const (
	reasonIdentifierUnverified = "identifier field cannot be modified"
	reasonImmutableUnverified  = "immutable field cannot be modified"
	reasonRequired             = "required field must be set to a non-empty value"
)

// applyMaskCase is one ApplyMask call: the raw mask (nil paths for an omitted
// mask), the request and stored resources, the options, and either the
// effective mask and the request after the call, or the error.
type applyMaskCase struct {
	name    string
	paths   []string
	req     proto.Message
	current proto.Message
	opts    []update.Option

	want    []string      // effective paths; nil when an error is expected
	wantMsg proto.Message // the request after the call; nil: as sent

	wantErr    error            // a sentinel the error matches
	errPath    string           // the path of a *fieldmask.ValidationError
	violations []FieldViolation // the violations of a *BehaviorError
}

// run calls ApplyMask and checks the result against tc.
func (tc applyMaskCase) run(t *testing.T) {
	t.Helper()

	msg := proto.Clone(tc.req)

	opts := tc.opts
	if tc.current != nil {
		opts = append([]update.Option{update.WithCurrent(tc.current)}, opts...)
	}

	got, err := update.ApplyMask(maskOf(tc.paths), msg, opts...)

	switch {
	case tc.wantErr != nil || tc.errPath != "" || tc.violations != nil:
		require.Error(t, err)
		require.Nil(t, got)
		requireProtoEqual(t, tc.req, msg) // untouched on any error

		if tc.wantErr != nil {
			require.ErrorIs(t, err, tc.wantErr)
		}

		if tc.errPath != "" {
			requireValidationPath(t, err, tc.errPath)
		}

		if tc.violations != nil {
			require.Equal(t, tc.violations, behaviorViolations(t, err))
		}
	default:
		require.NoError(t, err)
		require.NotNil(t, got, "the effective mask is never nil")
		require.Equal(t, tc.want, got.ToPaths())

		want := tc.wantMsg
		if want == nil {
			want = tc.req
		}

		requireProtoEqual(t, want, msg)
	}
}

// requireProtoEqual asserts that got equals want.
func requireProtoEqual(t *testing.T, want, got proto.Message) {
	t.Helper()

	require.True(t, proto.Equal(want, got), "want\n%s\ngot\n%s", prototext.Format(want), prototext.Format(got))
}

// runApplyMaskCases runs every case in parallel.
func runApplyMaskCases(t *testing.T, cases []applyMaskCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestApplyMask_Presence pins the AIP-134 modes decided on the mask as sent:
// an omitted mask applies the implied one, an empty mask is a no-op unless
// WithEmptyMaskImplied, a mask whose every path is dropped is a no-op, and
// "*" alone is a full replacement.
func TestApplyMask_Presence(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{Id: "r", Description: "d", Profile: &pb.Profile{DisplayName: "n", UpdatedAt: "u"}, CreateTime: "c"}
	implied := &pb.Resource{Id: "r", Description: "d", Profile: &pb.Profile{DisplayName: "n"}}
	immutable := &pb.Resource{Description: "d", TenantId: "t"}
	full := &pb.Resource{Id: "r", Name: "n", Slug: "s", TenantId: "t", Password: "p", Description: "d", CreateTime: "c",
		Profile: &pb.Profile{Id: "p", DisplayName: "n", UpdatedAt: "u"}}
	stored := &pb.Resource{Id: "r", Name: "old", Slug: "s", TenantId: "t", Description: "old", Profile: &pb.Profile{Id: "p"}}

	runApplyMaskCases(t, []applyMaskCase{
		{name: "omitted: the implied mask", paths: nil, req: req, want: []string{"description", "profile.display_name"}, wantMsg: implied},
		{name: "empty: a no-op", paths: []string{}, req: req, want: []string{}},
		{name: "empty implied on request", paths: []string{}, req: req, opts: []update.Option{update.WithEmptyMaskImplied(true)},
			want: []string{"description", "profile.display_name"}, wantMsg: implied},
		{name: "omitted with nothing set", paths: nil, req: &pb.Resource{Id: "r"}, want: []string{}},
		{name: "every path dropped: a no-op, never implied", paths: []string{"create_time", "profile.updated_at"}, req: req,
			opts: []update.Option{update.WithEmptyMaskImplied(true)}, want: []string{}},
		{name: "omitted with an IMMUTABLE value and no stored resource", paths: nil, req: immutable,
			violations: []FieldViolation{{Path: "tenant_id", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: reasonImmutableUnverified}}},
		{name: "omitted with an unchanged IMMUTABLE value", paths: nil, req: immutable, current: &pb.Resource{TenantId: "t"},
			want: []string{"description"}, wantMsg: &pb.Resource{Description: "d"}},
		{name: "wildcard with an IMMUTABLE value and no stored resource", paths: []string{"*"}, req: immutable,
			violations: []FieldViolation{
				{Path: "name", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired},
				{Path: "tenant_id", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: reasonImmutableUnverified},
			}},
		{name: "wildcard: every field but the protected ones", paths: []string{"*"}, req: full, current: stored,
			want:    []string{"aliases", "description", "labels", "name", "password", "profile.display_name", "profile.secret"},
			wantMsg: &pb.Resource{Id: "r", Name: "n", Password: "p", Description: "d", Profile: &pb.Profile{Id: "p", DisplayName: "n"}}},
		{name: "wildcard with a REQUIRED field empty", paths: []string{"*"}, req: &pb.Resource{Slug: "s"}, current: &pb.Resource{Slug: "s"},
			violations: []FieldViolation{{Path: "name", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}},
	})
}

// TestApplyMask_Paths pins the AIP-203 rules on explicit paths and the
// effective mask written back: parents expanded so no path covers a
// protected field, OUTPUT_ONLY and unchanged IMMUTABLE or IDENTIFIER paths
// dropped, the IDENTIFIER entries Apply keeps in the request left out, and
// INPUT_ONLY fields kept.
func TestApplyMask_Paths(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{
		Id: "r", Description: "d", Password: "p", TenantId: "t", CreateTime: "c",
		Profile: &pb.Profile{Id: "p", DisplayName: "n", UpdatedAt: "u", Secret: "s"},
	}

	runApplyMaskCases(t, []applyMaskCase{
		{name: "parent over IDENTIFIER, OUTPUT_ONLY and INPUT_ONLY", paths: []string{"profile"}, req: req,
			want:    []string{"profile.display_name", "profile.secret"},
			wantMsg: &pb.Resource{Id: "r", Profile: &pb.Profile{Id: "p", DisplayName: "n", Secret: "s"}}},
		{name: "covered IDENTIFIER without a stored resource", paths: []string{"profile", "profile.id"}, req: req,
			violations: []FieldViolation{{Path: "profile.id", Behavior: annotations.FieldBehavior_IDENTIFIER, Reason: reasonIdentifierUnverified}}},
		{name: "covered IDENTIFIER, child first", paths: []string{"profile.id", "profile"}, req: req,
			violations: []FieldViolation{{Path: "profile.id", Behavior: annotations.FieldBehavior_IDENTIFIER, Reason: reasonIdentifierUnverified}}},
		{name: "covered IDENTIFIER unchanged", paths: []string{"profile", "profile.id"}, req: req, current: proto.Clone(req),
			want:    []string{"profile.display_name", "profile.secret"},
			wantMsg: &pb.Resource{Id: "r", Profile: &pb.Profile{Id: "p", DisplayName: "n", Secret: "s"}}},
		{name: "covered unknown path", paths: []string{"description", "description.no_such"}, req: req, errPath: "description.no_such"},
		{name: "covered index", paths: []string{"aliases", "aliases.0.id"}, req: req, errPath: "aliases.0.id"},
		{name: "top-level INPUT_ONLY kept", paths: []string{"password"}, req: req, want: []string{"password"},
			wantMsg: &pb.Resource{Id: "r", Password: "p"}},
		{name: "OUTPUT_ONLY dropped", paths: []string{"create_time", "description"}, req: req, want: []string{"description"},
			wantMsg: &pb.Resource{Id: "r", Description: "d"}},
		{name: "only OUTPUT_ONLY: a no-op", paths: []string{"create_time", "profile.updated_at", "audit"}, req: req, want: []string{}},
		{name: "IMMUTABLE without a stored resource", paths: []string{"tenant_id"}, req: req,
			violations: []FieldViolation{{Path: "tenant_id", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: reasonImmutableUnverified}}},
		{name: "IMMUTABLE unchanged: a no-op", paths: []string{"tenant_id"}, req: req, current: &pb.Resource{Id: "r", TenantId: "t"}, want: []string{}},
		{name: "IDENTIFIER named unchanged", paths: []string{"id", "description"}, req: req, current: &pb.Resource{Id: "r"},
			want: []string{"description"}, wantMsg: &pb.Resource{Id: "r", Description: "d"}},
		{name: "JSON spelling", paths: []string{"profile.displayName"}, req: req, want: []string{"profile.display_name"},
			wantMsg: &pb.Resource{Id: "r", Profile: &pb.Profile{Id: "p", DisplayName: "n"}}},
		{name: "unknown path", paths: []string{"no_such"}, req: req, errPath: "no_such"},
		{name: "REQUIRED field left empty", paths: []string{"name"}, req: req,
			violations: []FieldViolation{{Path: "name", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}},
		{name: "unset field in the mask is cleared", paths: []string{"source_url"}, req: req, want: []string{"source_url"},
			wantMsg: &pb.Resource{Id: "r"}},
	})
}

// TestApplyMask_Collections pins the replacement of whole lists, maps and
// map entries — new elements whose IMMUTABLE and IDENTIFIER values are kept
// as sent and whose OUTPUT_ONLY values are cleared — and the in-place update
// of element sub-paths.
func TestApplyMask_Collections(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{
		Aliases: []*pb.Profile{{Id: "a", DisplayName: "x", UpdatedAt: "u"}},
		Labels:  map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y", UpdatedAt: "u"}, "j": {DisplayName: "z"}},
	}
	clean := &pb.Resource{
		Aliases: []*pb.Profile{{Id: "a", DisplayName: "x"}},
		Labels:  map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y"}, "j": {DisplayName: "z"}},
	}
	account := &pb.Account{Members: []*pb.Owner{{Org: "o"}}, Delegates: map[string]*pb.Owner{"k": {Email: "e", Org: "o"}}}

	runApplyMaskCases(t, []applyMaskCase{
		{name: "whole list", paths: []string{"aliases"}, req: req, want: []string{"aliases"},
			wantMsg: &pb.Resource{Aliases: clean.GetAliases()}},
		{name: "whole map", paths: []string{"labels"}, req: req, want: []string{"labels"}, wantMsg: &pb.Resource{Labels: clean.GetLabels()}},
		{name: "map entry", paths: []string{"labels.k"}, req: req, want: []string{"labels.k"},
			wantMsg: &pb.Resource{Labels: map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y"}}}},
		{name: "list wildcard", paths: []string{"aliases.*"}, req: req, want: []string{"aliases.*"},
			wantMsg: &pb.Resource{Aliases: clean.GetAliases()}},
		{name: "element sub-path keeps the element IDENTIFIER", paths: []string{"aliases.*.display_name"}, req: req,
			want: []string{"aliases.*.display_name"}, wantMsg: &pb.Resource{Aliases: clean.GetAliases()}},
		{name: "map entry sub-path", paths: []string{"labels.k.display_name"}, req: req, want: []string{"labels.k.display_name"},
			wantMsg: &pb.Resource{Labels: map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y"}}}},
		{name: "element IDENTIFIER named", paths: []string{"labels.k.id"}, req: req,
			violations: []FieldViolation{{Path: "labels.k.id", Behavior: annotations.FieldBehavior_IDENTIFIER, Reason: reasonIdentifierUnverified}}},
		{name: "element OUTPUT_ONLY: a no-op", paths: []string{"aliases.*.updated_at"}, req: req, want: []string{}},
		{name: "REQUIRED left empty in a replaced element", paths: []string{"members"}, req: account,
			violations: []FieldViolation{{Path: "members.0.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}},
		{name: "IMMUTABLE of a replaced element kept as new", paths: []string{"delegates"}, req: account, want: []string{"delegates"},
			wantMsg: &pb.Account{Delegates: account.GetDelegates()}},
		{name: "element IMMUTABLE named", paths: []string{"delegates.*.org"}, req: account,
			violations: []FieldViolation{{Path: "delegates.*.org", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: reasonImmutableUnverified}}},
		{name: "covered element sub-path", paths: []string{"members", "members.*.org"}, req: &pb.Account{Members: []*pb.Owner{{Email: "e", Org: "o"}}},
			violations: []FieldViolation{{Path: "members.*.org", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: reasonImmutableUnverified}}},
	})
}

// TestApplyMask_Validation pins the order of the checks — limits on the raw
// paths, duplicates included; the grammar of each raw path; then its schema
// — and the path validation modes: Enforce refuses, Warn reports and drops,
// Disabled drops; an index is refused in every mode.
func TestApplyMask_Validation(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{Description: "d", Profile: &pb.Profile{DisplayName: "n"}}
	described := &pb.Resource{Description: "d"}
	disabled := []update.Option{update.WithPathValidation(update.PathValidationDisabled)}
	warn := []update.Option{update.WithPathValidation(update.PathValidationWarn)}

	runApplyMaskCases(t, []applyMaskCase{
		{name: "path count with duplicates", paths: []string{"description", "description", "description"}, req: req,
			opts: []update.Option{update.WithMaxPaths(2)}, wantErr: fieldmask.ErrMaxPathsExceeded},
		{name: "duplicates within the count", paths: []string{"description", "description"}, req: req,
			opts: []update.Option{update.WithMaxPaths(2)}, want: []string{"description"}, wantMsg: described},
		{name: "path depth", paths: []string{"profile.display_name"}, req: req, opts: []update.Option{update.WithMaxPathDepth(1)},
			wantErr: fieldmask.ErrMaxPathDepthExceeded, errPath: "profile.display_name"},
		{name: "path length", paths: []string{"description"}, req: req, opts: []update.Option{update.WithMaxPathLength(4)},
			wantErr: fieldmask.ErrMaxPathLengthExceeded, errPath: "description"},
		{name: "limits hold when disabled", paths: []string{"a", "b"}, req: req,
			opts: append([]update.Option{update.WithMaxPaths(1)}, disabled...), wantErr: fieldmask.ErrMaxPathsExceeded},
		{name: "limit before grammar", paths: []string{"", "x.", "y"}, req: req, opts: []update.Option{update.WithMaxPaths(2)},
			wantErr: fieldmask.ErrMaxPathsExceeded},
		{name: "empty path", paths: []string{"description", ""}, req: req, errPath: "", wantErr: fieldmask.ErrInvalidPath},
		{name: "malformed path", paths: []string{"profile..display_name"}, req: req, errPath: "profile..display_name"},
		{name: "grammar before schema", paths: []string{"no_such", "a."}, req: req, errPath: "a."},
		{name: "wildcard with a path", paths: []string{"*", "description"}, req: req, errPath: "*", wantErr: fieldmask.ErrWildcardNotAlone},
		{name: "wildcard twice", paths: []string{"*", "*"}, req: req, errPath: "*", wantErr: fieldmask.ErrWildcardNotAlone},
		{name: "disabled drops grammar failures", paths: []string{"", "a.", "*", "description"}, req: req, opts: disabled,
			want: []string{"description"}, wantMsg: described},
		{name: "disabled drops unknown paths", paths: []string{"no_such", "description", "description.x"}, req: req, opts: disabled,
			want: []string{"description"}, wantMsg: described},
		{name: "disabled: wildcard twice is a no-op", paths: []string{"*", "*"}, req: req, opts: disabled, want: []string{}},
		{name: "warn without a reporter drops", paths: []string{"no_such", "description"}, req: req, opts: warn,
			want: []string{"description"}, wantMsg: described},
		{name: "index refused when disabled", paths: []string{"aliases.0.id", "description"}, req: req, opts: disabled, errPath: "aliases.0.id"},
		{name: "atomic sub-path refused when disabled", paths: []string{"profile", "profile.display_name"}, req: req,
			opts: append([]update.Option{update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
				return md.FullName() == "fieldbehaviortest.v1.Profile"
			})}, disabled...), errPath: "profile"},
	})
}

// TestApplyMask_Reporter pins that PathValidationWarn reports every path it
// drops, in order, and applies the others.
func TestApplyMask_Reporter(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		reported []string
	)

	report := func(ve *fieldmask.ValidationError) {
		mu.Lock()
		defer mu.Unlock()

		reported = append(reported, ve.Path)
	}

	msg := &pb.Resource{Description: "d", Name: "n"}
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*", "", "no_such", "description", "*", "description.x"}}, msg,
		update.WithPathValidation(update.PathValidationWarn), update.WithPathValidationReporter(report))
	require.NoError(t, err)
	require.Equal(t, []string{"description"}, got.ToPaths())
	require.Equal(t, []string{"*", "", "*", "no_such", "description.x"}, reported, "grammar first, then schema")
	requireProtoEqual(t, &pb.Resource{Description: "d"}, msg)
}

// TestApplyMask_Strict pins WithStrict: every path is enforced whatever
// WithPathValidation says, and every refusal is a *StrictError naming the
// offending path, or the mask field for a mask-level one.
func TestApplyMask_Strict(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{Description: "d", Profile: &pb.Profile{Id: "p", DisplayName: "n"}}

	for _, tc := range []struct {
		name      string
		paths     []string
		msg       proto.Message
		opts      []update.Option
		path      string
		reason    string
		sentinels []error
	}{
		{name: "unknown path when disabled", paths: []string{"no_such"}, msg: req,
			opts: []update.Option{update.WithPathValidation(update.PathValidationDisabled)}, path: "no_such",
			reason: "field 'no_such' does not exist in message fieldbehaviortest.v1.Resource", sentinels: []error{fieldmask.ErrInvalidPath}},
		{name: "covered identifier", paths: []string{"profile", "profile.id"}, msg: req, path: "profile.id",
			reason: reasonIdentifierUnverified, sentinels: []error{update.ErrBehaviorViolation}},
		{name: "path count", paths: []string{"a", "b"}, msg: req, opts: []update.Option{update.WithMaxPaths(1), update.WithUpdateMaskFieldName("update_mask")},
			path: "update_mask", reason: "the mask has 2 paths, more than the limit of 1", sentinels: []error{fieldmask.ErrMaxPathsExceeded}},
		{name: "wildcard twice", paths: []string{"*", "*"}, msg: req, path: "*", reason: `"*" must be the only path`,
			sentinels: []error{fieldmask.ErrWildcardNotAlone}},
		{name: "missing resource", paths: []string{"description"}, path: "updateMask", reason: update.ErrMessageMismatch.Error(),
			sentinels: []error{update.ErrMessageMismatch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var msg proto.Message
			if tc.msg != nil {
				msg = proto.Clone(tc.msg)
			}

			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: tc.paths}, msg, append(tc.opts, update.WithStrict(true))...)
			require.Nil(t, got)

			se, ok := errors.AsType[*update.StrictError](err)
			require.True(t, ok, "%v", err)
			require.ErrorIs(t, err, update.ErrInvalidUpdateMask)

			for _, s := range tc.sentinels {
				require.ErrorIs(t, err, s)
			}

			require.Equal(t, tc.path, se.Violations[0].Path)
			require.Equal(t, tc.reason, se.Violations[0].Reason)

			if tc.msg != nil {
				requireProtoEqual(t, tc.msg, msg)
			}
		})
	}

	// On success WithStrict applies the mask as without it.
	msg := proto.Clone(req)
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"profile"}}, msg, update.WithStrict(true))
	require.NoError(t, err)
	require.Equal(t, []string{"profile.display_name", "profile.secret"}, got.ToPaths())
	requireProtoEqual(t, &pb.Resource{Profile: &pb.Profile{Id: "p", DisplayName: "n"}}, msg)
}

// TestApplyMask_Mismatch pins that a missing request resource and a stored
// resource of another type are ErrMessageMismatch, whatever the mask.
func TestApplyMask_Mismatch(t *testing.T) {
	t.Parallel()

	for _, raw := range []*fieldmaskpb.FieldMask{nil, {}, {Paths: []string{"description"}}} {
		_, err := update.ApplyMask(raw, nil)
		require.ErrorIs(t, err, update.ErrMessageMismatch)

		_, err = update.ApplyMask(raw, (*pb.Resource)(nil))
		require.ErrorIs(t, err, update.ErrMessageMismatch)

		msg := &pb.Resource{Description: "d"}
		_, err = update.ApplyMask(raw, msg, update.WithCurrent(&pb.Account{}))
		require.ErrorIs(t, err, update.ErrMessageMismatch)
		requireProtoEqual(t, &pb.Resource{Description: "d"}, msg)
	}
}

// TestApplyMask_MatchesApply pins that ApplyMask mutates the request exactly
// as Apply does for a mask a FieldMask can hold, and returns Apply's cleaned
// mask without the IDENTIFIER entries Apply adds.
func TestApplyMask_MatchesApply(t *testing.T) {
	t.Parallel()

	req := &pb.Resource{
		Id: "r", Name: "n", Description: "d", Password: "p", CreateTime: "c",
		Profile: &pb.Profile{Id: "p", DisplayName: "n", UpdatedAt: "u", Secret: "s"},
		Aliases: []*pb.Profile{{Id: "a", DisplayName: "x", UpdatedAt: "u"}},
		Labels:  map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y"}},
	}

	for _, paths := range [][]string{
		nil, {}, {"*"}, {"profile"}, {"profile.display_name", "description"}, {"aliases.*.display_name", "labels.k.display_name"},
		{"aliases", "labels"}, {"create_time"}, {"password", "name"},
	} {
		viaApply := proto.Clone(req)
		mask, err := fieldmask.FromProtoFieldMask(maskOf(paths))
		require.NoError(t, err)
		require.NoError(t, update.Apply(mask, viaApply))

		viaApplyMask := proto.Clone(req)
		got, err := update.ApplyMask(maskOf(paths), viaApplyMask)
		require.NoError(t, err)
		requireProtoEqual(t, viaApply, viaApplyMask)

		if mask != nil {
			update.DropIdentifiers(mask, req)
			require.Equal(t, mask.ToPaths(), got.ToPaths(), "%v", paths)
		}
	}
}

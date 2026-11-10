// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// withOwner returns storedAccount with its owner replaced.
func withOwner(owner *pb.Owner) *pb.Account {
	a := storedAccount()
	a.Owner = owner

	return a
}

// TestAtomicReplacementKeepsProtectedValues pins the exact case of a singular
// message of an atomic type (Owner, under WithAtomicMessage) named whole:
// the merge copies it whole, so its IMMUTABLE org must equal the stored one.
// A changed or cleared org is refused, and without the stored resource any
// replacement is, by ApplyMask, Apply, ValidateStrict, Spec.Plan and
// Patch.Run, whether owner is named, replaced by "*" or reached through a
// replaced parent; an equal org is accepted.
func TestAtomicReplacementKeepsProtectedValues(t *testing.T) {
	t.Parallel()

	atomic := atomicNames("fieldbehaviortest.v1.Owner")
	stored := withOwner(&pb.Owner{Email: "e@example.com", Org: "original"})

	for _, tc := range []struct {
		name    string
		path    string
		req     *pb.Account
		current *pb.Account
		field   string // the refused path; "" when accepted
	}{
		{name: "changed", path: "owner", req: withOwner(&pb.Owner{Email: "e@example.com", Org: "changed"}), current: stored, field: "owner.org"},
		{name: "cleared", path: "owner", req: withOwner(&pb.Owner{Email: "e@example.com"}), current: stored, field: "owner.org"},
		{name: "unchanged", path: "owner", req: withOwner(&pb.Owner{Email: "new@example.com", Org: "original"}), current: stored},
		{name: "without the stored resource", path: "owner", req: withOwner(&pb.Owner{Email: "e@example.com", Org: "original"}), field: "owner.org"},
		{name: "full replacement", path: "*", req: withOwner(&pb.Owner{Email: "e@example.com", Org: "changed"}), current: stored, field: "owner.org"},
		{name: "full replacement unchanged", path: "*", req: proto.Clone(stored).(*pb.Account), current: stored},
		{
			name: "parent replacement", path: "parent",
			req:     &pb.Account{Parent: withOwner(&pb.Owner{Email: "e@example.com", Org: "changed"})},
			current: &pb.Account{Parent: stored}, field: "parent.owner.org",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := []update.Option{atomic}
			if tc.current != nil {
				opts = append(opts, update.WithCurrent(tc.current))
			}

			spec, err := update.NewSpec(&pb.Account{}, atomic)
			require.NoError(t, err)

			planOpts := []update.Option{}
			if tc.current != nil {
				planOpts = append(planOpts, update.WithCurrent(tc.current))
			}

			_, applyMaskErr := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.path}}, proto.Clone(tc.req), opts...)
			applyErr := update.Apply(fieldmask.FromPaths(tc.path), proto.Clone(tc.req), opts...)
			_, strictErr := update.ValidateStrict(fieldmask.FromPaths(tc.path), tc.req, opts...)
			_, planErr := spec.Plan(tc.req, &fieldmaskpb.FieldMask{Paths: []string{tc.path}}, planOpts...)

			if tc.field == "" {
				require.NoError(t, applyMaskErr)
				require.NoError(t, applyErr)
				require.NoError(t, strictErr)
				require.NoError(t, planErr)

				return
			}

			require.Contains(t, violationPaths(t, applyMaskErr), tc.field)
			require.Contains(t, violationPaths(t, applyErr), tc.field)
			require.ErrorIs(t, strictErr, update.ErrBehaviorViolation)
			require.ErrorIs(t, planErr, update.ErrProtectedField)

			if tc.current != nil && tc.path == "owner" {
				s := &accountStore{cur: proto.Clone(tc.current).(*pb.Account)}
				p := s.patch(t)
				p.Spec = spec

				_, err := p.Run(t.Context(), tc.req, "", &fieldmaskpb.FieldMask{Paths: []string{tc.path}})
				require.ErrorIs(t, err, update.ErrProtectedField)
				require.Nil(t, s.saved)
			}
		})
	}
}

// TestAtomicReplacementRequiredImmutable pins that an atomic value written
// whole is checked as written: Owner.org, REQUIRED and IMMUTABLE, left empty
// in an owner replaced whole is reported though the stored org is empty too.
func TestAtomicReplacementRequiredImmutable(t *testing.T) {
	t.Parallel()

	atomic := atomicNames("fieldbehaviortest.v1.Owner")
	stored := withOwner(&pb.Owner{Email: "e@example.com"})
	req := withOwner(&pb.Owner{Email: "e@example.com"})

	for _, path := range []string{"owner", "*"} {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, proto.Clone(req), atomic, requiredImmutableOrg,
			update.WithCurrent(stored))
		require.Contains(t, behaviorViolations(t, err),
			FieldViolation{Path: "owner.org", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}, path)
	}
}

// TestAtomicKeyedElementIsNewValue pins the documented choice for a keyed
// element of an atomic type named whole ("delegates.k" with Owner atomic):
// it is a new value, as any element named whole, since MergePaths replaces
// the stored entry with the request's, so its IMMUTABLE org is not compared
// and the merged entry is the request's.
func TestAtomicKeyedElementIsNewValue(t *testing.T) {
	t.Parallel()

	atomic := atomicNames("fieldbehaviortest.v1.Owner")
	stored := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e@example.com", Org: "original"}}}
	req := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e@example.com", Org: "changed"}}}

	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delegates.k"}}, proto.Clone(req), atomic, update.WithCurrent(stored))
	require.NoError(t, err)

	merged := proto.Clone(stored)
	require.NoError(t, update.MergePaths(merged, req, got.ToPaths(), atomic))
	requireProtoEqual(t, req, merged)
}

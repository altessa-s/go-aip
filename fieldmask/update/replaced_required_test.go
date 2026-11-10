// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// requiredImmutableOrg resolves Owner.org as REQUIRED and IMMUTABLE, every
// other field by its annotations.
var requiredImmutableOrg = update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	if fd.FullName() == "fieldbehaviortest.v1.Owner.org" {
		return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED, annotations.FieldBehavior_IMMUTABLE}
	}

	return fieldbehavior.AnnotationResolver{}.Resolve(fd)
}))

// TestReplacementChecksNewElementsAsOnCreate pins that a collection replaced
// whole through "*" or through a singular parent named whole holds new
// elements whose REQUIRED fields are checked as on create, a REQUIRED and
// IMMUTABLE one included, as when the collection is named itself.
func TestReplacementChecksNewElementsAsOnCreate(t *testing.T) {
	t.Parallel()

	withEmptyOrg := func(a *pb.Account) *pb.Account {
		a.Members = []*pb.Owner{{Email: "m@example.com"}}
		return a
	}

	required := func(path string) []FieldViolation {
		return []FieldViolation{{Path: path, Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}
	}

	parentReq := &pb.Account{Parent: withEmptyOrg(storedAccount())}
	parentCur := &pb.Account{Parent: storedAccount()}

	runApplyMaskCases(t, []applyMaskCase{
		{name: "collection named", paths: []string{"members"}, req: withEmptyOrg(storedAccount()), current: storedAccount(),
			opts: []update.Option{requiredImmutableOrg}, violations: required("members.0.org")},
		{name: "full replacement", paths: []string{"*"}, req: withEmptyOrg(storedAccount()), current: storedAccount(),
			opts: []update.Option{requiredImmutableOrg}, violations: required("members.0.org")},
		{name: "singular parent", paths: []string{"parent"}, req: parentReq, current: parentCur,
			opts: []update.Option{requiredImmutableOrg}, violations: required("parent.members.0.org")},
		{name: "IMMUTABLE of a replaced message still skipped", paths: []string{"parent"},
			req: &pb.Account{Parent: func() *pb.Account { a := storedAccount(); a.Owner.Org = ""; return a }()}, current: parentCur,
			opts: []update.Option{requiredImmutableOrg},
			want: []string{
				"parent.attributes", "parent.card", "parent.delegates", "parent.display_name", "parent.emails", "parent.enabled", "parent.etag",
				"parent.key", "parent.members", "parent.owner.email", "parent.owner.note", "parent.quota", "parent.state",
			},
			wantMsg: &pb.Account{Parent: func() *pb.Account {
				a := storedAccount()
				a.Owner = &pb.Owner{Email: a.GetOwner().GetEmail()}
				a.UpdateTime, a.Region = "", ""

				return a
			}()}},
	})

	t.Run("Apply, ValidateStrict and Spec.Plan", func(t *testing.T) {
		t.Parallel()

		req := withEmptyOrg(storedAccount())

		err := update.Apply(fieldmask.FromPaths("*"), proto.Clone(req), update.WithCurrent(storedAccount()), requiredImmutableOrg)
		require.Equal(t, required("members.0.org"), behaviorViolations(t, err))

		_, err = update.ValidateStrict(fieldmask.FromPaths("*"), req, update.WithCurrent(storedAccount()), requiredImmutableOrg)
		require.ErrorIs(t, err, update.ErrBehaviorViolation)

		spec, err := update.NewSpec(&pb.Account{}, requiredImmutableOrg)
		require.NoError(t, err)

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"*"}}, update.WithCurrent(storedAccount()))
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})
}

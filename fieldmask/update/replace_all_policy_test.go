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

// requiredEtag resolves Account.etag as REQUIRED, every other field by its
// annotations.
var requiredEtag = update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	if fd.FullName() == "fieldbehaviortest.v1.Account.etag" {
		return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
	}

	return fieldbehavior.AnnotationResolver{}.Resolve(fd)
}))

// TestReplaceAllKeepsPolicyProtectedRequired pins that "*" does not require a
// REQUIRED top-level field the policy protects — named by
// WithImmutableFields or WithIdentityFields (etag by default) — when the
// request leaves it unset: the replacement keeps its stored value, as for an
// annotated IMMUTABLE or IDENTIFIER field. Nested REQUIRED fields of such a
// field are not checked either; a changed value is still refused.
func TestReplaceAllKeepsPolicyProtectedRequired(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		clear func(*pb.Account)
		opts  []update.Option
	}{
		{name: "WithImmutableFields", clear: func(a *pb.Account) { a.DisplayName = "" }, opts: []update.Option{update.WithImmutableFields("display_name")}},
		{name: "default identity etag", clear: func(a *pb.Account) { a.Etag = "" }, opts: []update.Option{requiredEtag}},
		{name: "immutable message", clear: func(a *pb.Account) { a.Owner = nil }, opts: []update.Option{update.WithImmutableFields("owner")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := storedAccount()
			tc.clear(req)

			opts := append([]update.Option{update.WithCurrent(storedAccount())}, tc.opts...)

			_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, proto.Clone(req), opts...)
			require.NoError(t, err)

			require.NoError(t, update.Apply(fieldmask.FromPaths("*"), proto.Clone(req), opts...))

			_, err = update.ValidateStrict(fieldmask.FromPaths("*"), req, opts...)
			require.NoError(t, err)

			spec, err := update.NewSpec(&pb.Account{}, tc.opts...)
			require.NoError(t, err)

			_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"*"}}, update.WithCurrent(storedAccount()))
			require.NoError(t, err)
		})
	}

	// A changed protected value is still refused.
	req := storedAccount()
	req.DisplayName = "other"
	_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, req, update.WithCurrent(storedAccount()),
		update.WithImmutableFields("display_name"))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

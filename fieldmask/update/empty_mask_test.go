// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// changedRequest is storedRequest with a changed mutable field, a changed
// IMMUTABLE field, a changed IDENTIFIER and a REQUIRED field left empty: any
// update that reads it either applies or rejects something, so only a no-op
// leaves it untouched without an error.
func changedRequest() *testpb.UpdateRequest {
	req := storedRequest()
	req.Description = new("new")
	req.Code = new("C2")
	req.ResourceName = new("requests/2")
	req.Name = nil

	return req
}

// TestFieldMask_Presence pins that the FieldMask API keeps the presence of an
// update_mask (AIP-134): omitted is a nil FieldMask, sent empty a non-nil
// empty one, through conversion and Clone.
func TestFieldMask_Presence(t *testing.T) {
	t.Parallel()

	t.Run("omitted", func(t *testing.T) {
		t.Parallel()

		mask, err := fieldmask.FromProtoFieldMask(nil)
		require.NoError(t, err)
		assert.Nil(t, mask)
		assert.True(t, mask.IsEmpty())
		assert.Nil(t, mask.Clone())
		assert.Nil(t, mask.ToProtoFieldMask())
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		mask, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{})
		require.NoError(t, err)
		require.NotNil(t, mask)
		assert.True(t, mask.IsEmpty())
		assert.NotNil(t, mask.Clone())
		assert.True(t, mask.Clone().IsEmpty())

		pbMask := mask.ToProtoFieldMask()
		require.NotNil(t, pbMask)
		assert.Empty(t, pbMask.GetPaths())
	})

	t.Run("wildcard", func(t *testing.T) {
		t.Parallel()

		mask, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}})
		require.NoError(t, err)
		assert.False(t, mask.IsEmpty())

		all, err := mask.SelectsAll()
		require.NoError(t, err)
		assert.True(t, all)
		assert.Equal(t, []string{"*"}, mask.ToProtoFieldMask().GetPaths())
	})
}

// TestApplyUpdateMask_EmptyMaskIsNoOp pins that an update_mask sent with no
// paths leaves the message and the mask untouched, checks no field behavior,
// and still rejects a stored resource of another type.
func TestApplyUpdateMask_EmptyMaskIsNoOp(t *testing.T) {
	t.Parallel()

	for name, opts := range map[string][]update.Option{
		"without the stored resource": nil,
		"with the stored resource":    {update.WithCurrent(storedRequest())},
		"with path validation":        {update.WithPathValidation(update.PathValidationEnforce)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := changedRequest()
			mask := fieldmask.FieldMask{}
			require.NoError(t, update.Apply(mask, req, opts...))
			assert.NotNil(t, mask)
			assert.True(t, mask.IsEmpty(), "nothing to update")
			assert.True(t, proto.Equal(changedRequest(), req), "the message is left untouched")
		})
	}

	t.Run("stored resource of another type", func(t *testing.T) {
		t.Parallel()

		req := changedRequest()
		err := update.Apply(fieldmask.FieldMask{}, req, update.WithCurrent(&testpb.Address{}))
		require.ErrorIs(t, err, update.ErrMessageMismatch)
		assert.True(t, proto.Equal(changedRequest(), req))
	})
}

// TestApplyUpdateMask_NilMaskIsImplied pins that a nil mask, an omitted
// update_mask, applies the implied mask of FromSetFieldsForUpdate.
func TestApplyUpdateMask_NilMaskIsImplied(t *testing.T) {
	t.Parallel()

	newReq := func() *testpb.UpdateRequest {
		req := storedRequest()
		req.Description = new("new")

		return req
	}
	current := update.WithCurrent(storedRequest())

	got := newReq()
	require.NoError(t, update.Apply(fieldmask.FieldMask(nil), got, current))

	want := newReq()
	implied := update.FromSetFields(want)
	require.NoError(t, update.Apply(implied, want, current))
	require.False(t, implied.IsEmpty())
	assert.True(t, proto.Equal(want, got), "a nil mask applies the implied mask")
	assert.Equal(t, "new", got.GetDescription())
	assert.False(t, has(got, "created_at"), "OUTPUT_ONLY values are cleared")

	t.Run("violations are still reported", func(t *testing.T) {
		t.Parallel()

		req := changedRequest()
		req.Name = new("Acme")
		err := update.Apply(fieldmask.FieldMask(nil), req, update.WithCurrent(storedRequest()))
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})

	t.Run("nothing populated", func(t *testing.T) {
		t.Parallel()

		req := &testpb.UpdateRequest{Id: "1", CreatedAt: new("client")}
		require.NoError(t, update.Apply(fieldmask.FieldMask(nil), req))
		assert.True(t, proto.Equal(&testpb.UpdateRequest{Id: "1"}, req),
			"an effective no-op still clears output-only input")
	})
}

// TestApplyUpdateMask_WildcardStillReplaces pins that "*" stays a full
// replacement next to the empty and nil masks.
func TestApplyUpdateMask_WildcardStillReplaces(t *testing.T) {
	t.Parallel()

	req := storedRequest()
	req.Description = nil
	mask := fieldmask.FromPaths("*")
	require.NoError(t, update.Apply(mask, req, update.WithCurrent(storedRequest())))
	assert.False(t, mask.IsEmpty())
	assert.True(t, mask.Contains("description"), "an unset field is cleared by the replacement")
	assert.False(t, has(req, "description"))
	assert.False(t, has(req, "created_at"))
}

// TestApplyUpdateMask_AllPathsDroppedIsNoOp pins that a mask whose every path
// is dropped updates nothing: the receiver comes back empty and the message
// untouched, as UpdatePlan.Empty reports for the same mask.
func TestApplyUpdateMask_AllPathsDroppedIsNoOp(t *testing.T) {
	t.Parallel()

	req := storedRequest()
	mask := fieldmask.FromPaths("created_at", "code", "resource_name")
	require.NoError(t, update.Apply(mask, req, update.WithCurrent(storedRequest())))
	assert.NotNil(t, mask)
	assert.True(t, mask.IsEmpty())
	assert.True(t, proto.Equal(withoutOutputOnly(t, storedRequest()), req))

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	plan, err := spec.Plan(storedRequest(), &fieldmaskpb.FieldMask{Paths: []string{"created_at", "code", "resource_name"}},
		update.WithCurrent(storedRequest()))
	require.NoError(t, err)
	assert.True(t, plan.Empty())
}

// TestValidateStrictUpdateMask_MaskPresence pins the strict variant for the
// nil, empty and "*" masks.
func TestValidateStrictUpdateMask_MaskPresence(t *testing.T) {
	t.Parallel()

	current := update.WithCurrent(storedRequest())

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		req := changedRequest()
		got, err := update.ValidateStrict(fieldmask.FieldMask{}, req, current)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, got.IsEmpty())
		assert.True(t, proto.Equal(changedRequest(), req))
	})

	t.Run("empty, stored resource of another type", func(t *testing.T) {
		t.Parallel()

		_, err := update.ValidateStrict(fieldmask.FieldMask{}, storedRequest(), update.WithCurrent(&testpb.Address{}))
		require.ErrorIs(t, err, update.ErrMessageMismatch)
	})

	t.Run("empty, missing resource", func(t *testing.T) {
		t.Parallel()

		_, err := update.ValidateStrict(fieldmask.FieldMask{}, nil)
		require.ErrorIs(t, err, update.ErrMessageMismatch)
	})

	t.Run("nil is the implied mask", func(t *testing.T) {
		t.Parallel()

		req := storedRequest()
		req.Description = new("new")
		got, err := update.ValidateStrict(nil, req, current)
		require.NoError(t, err)

		want := []string{"address.city", "address.street", "description", "name", "options.color", "priority", "shipping_address.city", "shipping_address.street", "status"}
		assert.Equal(t, want, got.ToPaths())
		assert.True(t, got.Contains("description"))
	})

	t.Run("nil reports violations", func(t *testing.T) {
		t.Parallel()

		req := changedRequest()
		req.Name = new("Acme")
		_, err := update.ValidateStrict(nil, req, current)
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})

	t.Run("wildcard", func(t *testing.T) {
		t.Parallel()

		got, err := update.ValidateStrict(fieldmask.FromPaths("*"), storedRequest(), current)
		require.NoError(t, err)
		assert.True(t, got.Contains("description"))
	})
}

// TestUpdateEntryPoints_AgreeOnNoOp pins that Apply, the strict
// variant and MaskSpec.Plan agree on whether an update changes nothing.
func TestUpdateEntryPoints_AgreeOnNoOp(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	tests := []struct {
		name  string
		paths []string
		empty bool
	}{
		{name: "empty", paths: []string{}, empty: true},
		{name: "dropped only", paths: []string{"created_at"}, empty: true},
		{name: "unchanged identifier", paths: []string{"resource_name"}, empty: true},
		{name: "mutable path", paths: []string{"description"}},
		{name: "wildcard", paths: []string{"*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := update.WithCurrent(storedRequest())

			plan, err := spec.Plan(storedRequest(), &fieldmaskpb.FieldMask{Paths: tt.paths}, current)
			require.NoError(t, err)
			assert.Equal(t, tt.empty, plan.Empty(), "Plan")

			strict, err := update.ValidateStrict(fieldmask.FromPaths(tt.paths...), storedRequest(), current)
			require.NoError(t, err)
			assert.Equal(t, tt.empty, strict.IsEmpty(), "ValidateStrictUpdateMask")

			req := storedRequest()
			mask := fieldmask.FromPaths(tt.paths...)
			require.NoError(t, update.Apply(mask, req, current))
			assert.Equal(t, tt.empty, mask.IsEmpty(), "Apply")

			if tt.empty {
				want := proto.Message(storedRequest())
				if len(tt.paths) > 0 {
					want = withoutOutputOnly(t, want)
				}

				assert.True(t, proto.Equal(want, req), "a no-op leaves the message untouched")
			}
		})
	}
}

// TestPatch_MaskPresence pins that Patch saves nothing for an empty mask and
// saves for the nil and "*" masks.
func TestPatch_MaskPresence(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	tests := []struct {
		name      string
		mask      *fieldmaskpb.FieldMask
		wantSaved bool
	}{
		{name: "empty", mask: &fieldmaskpb.FieldMask{}},
		{name: "nil", mask: nil, wantSaved: true},
		{name: "wildcard", mask: &fieldmaskpb.FieldMask{Paths: []string{"*"}}, wantSaved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var saved *testpb.UpdateRequest

			patch := update.Patch[*testpb.UpdateRequest, *testpb.UpdateRequest, *int64]{
				Field: "request", Spec: spec,
				Precondition: update.Int64Precondition[*testpb.UpdateRequest]{
					Field: "request.etag", Version: func(*testpb.UpdateRequest) int64 { return 1 },
				},
				Read:    func(context.Context) (*testpb.UpdateRequest, error) { return storedRequest(), nil },
				ToProto: func(r *testpb.UpdateRequest) *testpb.UpdateRequest { return proto.Clone(r).(*testpb.UpdateRequest) },
				Save: func(_ context.Context, merged *testpb.UpdateRequest, _ update.Plan, _ *int64) (*testpb.UpdateRequest, error) {
					saved = merged
					return merged, nil
				},
			}

			in := storedRequest()
			in.Description = new("new")

			out, err := patch.Run(t.Context(), in, "", tt.mask)
			require.NoError(t, err)

			if !tt.wantSaved {
				assert.Nil(t, saved, "an empty mask saves nothing")
				assert.True(t, proto.Equal(storedRequest(), out), "the stored resource is returned")

				return
			}

			require.NotNil(t, saved)
			assert.Equal(t, "new", out.GetDescription())
		})
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// storedRequest returns an UpdateRequest as a service would store it: every
// REQUIRED field set, and a value in every protected field.
func storedRequest() *testpb.UpdateRequest {
	return &testpb.UpdateRequest{
		Id:              "1",
		Name:            new("Acme"),
		ResourceName:    new("requests/1"),
		Description:     new("old"),
		Address:         &testpb.Address{City: new("Belgrade"), Street: new("Main")},
		ShippingAddress: &testpb.Address{City: new("Novi Sad"), Street: new("Old")},
		Code:            new("C1"),
		CreatedAt:       new("server"),
		Priority:        new(int32(3)),
		Options:         &testpb.Options{Color: new("red")},
		Status:          "active",
	}
}

// has reports whether msg populates its field name.
func has(msg proto.Message, name string) bool {
	m := msg.ProtoReflect()

	return m.Has(m.Descriptor().Fields().ByName(protoreflect.Name(name)))
}

// TestUpdateEntryPoints_MaskPolicyParity pins that Apply,
// ValidateStrictUpdateMask and MaskSpec.Plan accept and refuse the same
// updates under one mask policy: a field of WithImmutableFields is
// IMMUTABLE and the default identity field id is never changed.
func TestUpdateEntryPoints_MaskPolicyParity(t *testing.T) {
	t.Parallel()

	policy := []update.Option{update.WithImmutableFields("description")}

	tests := []struct {
		name    string
		paths   []string
		mutate  func(*testpb.UpdateRequest)
		current bool
		wantErr bool
	}{
		{name: "changed policy-immutable path", paths: []string{"description"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Description = new("new") }, wantErr: true},
		{name: "unchanged policy-immutable path", paths: []string{"description", "status"}, current: true},
		{name: "policy-immutable path without the stored resource", paths: []string{"description"}, wantErr: true},
		{name: "changed identity path", paths: []string{"id"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Id = "2" }, wantErr: true},
		{name: "changed identity field the mask does not name", paths: []string{"status"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Id = "2" }},
		{name: "full replacement changing a policy-immutable field", paths: []string{"*"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Description = new("new") }, wantErr: true},
		{name: "full replacement leaving a policy-immutable field unset", paths: []string{"*"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Description = nil }},
		{name: "path in JSON spelling", paths: []string{"shippingAddress.street"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.ShippingAddress.Street = new("New") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedRequest()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			opts := append([]update.Option(nil), policy...)
			if tt.current {
				opts = append(opts, update.WithCurrent(storedRequest()))
			}

			spec, err := update.NewSpec(req, policy...)
			require.NoError(t, err)

			_, planErr := spec.Plan(req, maskOf(tt.paths), opts...)
			_, strictErr := update.ValidateStrict(fieldmask.FromPaths(tt.paths...), req, opts...)
			applyErr := update.Apply(fieldmask.FromPaths(tt.paths...), proto.Clone(req), opts...)

			assert.Equal(t, tt.wantErr, planErr != nil, "Plan: %v", planErr)
			assert.Equal(t, tt.wantErr, strictErr != nil, "ValidateStrictUpdateMask: %v", strictErr)
			assert.Equal(t, tt.wantErr, applyErr != nil, "Apply: %v", applyErr)
		})
	}
}

// TestApplyUpdateMask_MaskPolicy pins the field behaviors the mask policy
// gives the top-level fields of Apply and ValidateStrictUpdateMask.
func TestApplyUpdateMask_MaskPolicy(t *testing.T) {
	t.Parallel()

	type violation struct {
		path     string
		behavior annotations.FieldBehavior
	}

	tests := []struct {
		name      string
		paths     []string
		mutate    func(*testpb.UpdateRequest)
		policy    []update.Option
		current   bool
		wantPaths []string
		want      []violation
	}{
		{
			name: "changed policy-immutable path", paths: []string{"description"}, current: true,
			policy: []update.Option{update.WithImmutableFields("description")},
			mutate: func(r *testpb.UpdateRequest) { r.Description = new("new") },
			want:   []violation{{"description", annotations.FieldBehavior_IMMUTABLE}},
		},
		{
			name: "unchanged policy-immutable path is ignored", paths: []string{"description", "status"}, current: true,
			policy:    []update.Option{update.WithImmutableFields("description")},
			wantPaths: []string{"id", "resource_name", "status"},
		},
		{
			name: "policy-immutable path without the stored resource", paths: []string{"description"},
			policy: []update.Option{update.WithImmutableFields("description")},
			want:   []violation{{"description", annotations.FieldBehavior_IMMUTABLE}},
		},
		{
			name: "changed identity path", paths: []string{"id"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Id = "2" },
			want:   []violation{{"id", annotations.FieldBehavior_IDENTIFIER}},
		},
		{
			name: "identity path without the stored resource", paths: []string{"id"},
			want: []violation{{"id", annotations.FieldBehavior_IDENTIFIER}},
		},
		{
			name: "unchanged identity path is ignored and kept as the address", paths: []string{"id", "status"}, current: true,
			wantPaths: []string{"id", "resource_name", "status"},
		},
		{
			name: "identity field the mask does not name is kept, not compared", paths: []string{"status"}, current: true,
			mutate:    func(r *testpb.UpdateRequest) { r.Id = "2" },
			wantPaths: []string{"id", "resource_name", "status"},
		},
		{
			name: "changed identity field of a custom policy", paths: []string{"status"}, current: true,
			policy: []update.Option{update.WithIdentityFields("status")},
			mutate: func(r *testpb.UpdateRequest) { r.Status = "closed" },
			want:   []violation{{"status", annotations.FieldBehavior_IDENTIFIER}},
		},
		{
			name: "no identity fields", paths: []string{"id"}, current: true,
			policy:    []update.Option{update.WithIdentityFields()},
			mutate:    func(r *testpb.UpdateRequest) { r.Id = "2" },
			wantPaths: []string{"id", "resource_name"},
		},
		{
			name: "annotated identifier still compared when unnamed", paths: []string{"status"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.ResourceName = new("requests/2") },
			want:   []violation{{"resource_name", annotations.FieldBehavior_IDENTIFIER}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedRequest()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			opts := append([]update.Option(nil), tt.policy...)
			if tt.current {
				opts = append(opts, update.WithCurrent(storedRequest()))
			}

			strictMask, strictErr := update.ValidateStrict(fieldmask.FromPaths(tt.paths...), req, opts...)

			before := proto.Clone(req)
			mask := fieldmask.FromPaths(tt.paths...)
			err := update.Apply(mask, req, opts...)

			if tt.want != nil {
				violations := behaviorViolations(t, err)
				got := make([]violation, 0, len(violations))

				for _, v := range violations {
					got = append(got, violation{v.Path, v.Behavior})
				}

				require.Equal(t, tt.want, got)
				require.True(t, proto.Equal(before, req), "a rejected update leaves the message untouched")
				require.ErrorIs(t, strictErr, update.ErrBehaviorViolation)

				return
			}

			require.NoError(t, err)
			require.NoError(t, strictErr)
			assert.Equal(t, tt.wantPaths, mask.ToPaths())

			// The strict mask holds the paths the update applies; the
			// address fields are added by Apply only.
			update.DropIdentifiers(mask, req, tt.policy...)
			assert.Equal(t, mask.ToPaths(), strictMask.ToPaths())
		})
	}
}

// TestApplyUpdateMask_PolicyIdentityKeptInMessage pins that an identity
// field of the policy survives the filter as the address of the resource.
func TestApplyUpdateMask_PolicyIdentityKeptInMessage(t *testing.T) {
	t.Parallel()

	req := storedRequest()
	mask := fieldmask.FromPaths("status")
	require.NoError(t, update.Apply(mask, req, update.WithCurrent(storedRequest())))

	assert.Equal(t, "1", req.GetId())
	assert.Equal(t, "active", req.GetStatus())
	assert.False(t, has(req, "description"), "a field outside the mask is cleared")

	update.DropIdentifiers(mask, req)
	assert.Equal(t, []string{"status"}, mask.ToPaths(), "DropIdentifiers removes the policy identity fields")
}

// TestApplyUpdateMask_FullReplacementClearsAbsentFields pins AIP-134's "*":
// a field the request leaves unset is cleared (absent, no presence), not
// written as a default, while the server-managed fields follow AIP-203.
func TestApplyUpdateMask_FullReplacementClearsAbsentFields(t *testing.T) {
	t.Parallel()

	req := &testpb.UpdateRequest{
		Id:        "1",
		Name:      new("Renamed"),
		Address:   &testpb.Address{City: new("Belgrade")},
		CreatedAt: new("client"),
	}

	for _, entry := range []struct {
		name string
		run  func(fieldmask.FieldMask, proto.Message) (fieldmask.FieldMask, error)
	}{
		{name: "Apply", run: func(m fieldmask.FieldMask, msg proto.Message) (fieldmask.FieldMask, error) {
			return m, update.Apply(m, msg, update.WithCurrent(storedRequest()))
		}},
		{name: "ValidateStrictUpdateMask", run: func(m fieldmask.FieldMask, msg proto.Message) (fieldmask.FieldMask, error) {
			return update.ValidateStrict(m, msg, update.WithCurrent(storedRequest()))
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			t.Parallel()

			msg := proto.Clone(req).(*testpb.UpdateRequest)
			mask, err := entry.run(fieldmask.FromPaths("*"), msg)
			require.NoError(t, err)

			for _, path := range []string{"description", "options", "priority", "shipping_address", "address", "tags", "status"} {
				assert.True(t, mask.Contains(path), "%s is replaced", path)
			}

			for _, path := range []string{"created_at", "code", "resource_name"} {
				assert.False(t, mask.Contains(path), "%s keeps its stored value", path)
			}
		})
	}

	msg := proto.Clone(req).(*testpb.UpdateRequest)
	require.NoError(t, update.Apply(fieldmask.FromPaths("*"), msg, update.WithCurrent(storedRequest())))

	for _, name := range []string{"description", "options", "priority", "shipping_address", "created_at", "code"} {
		assert.False(t, has(msg, name), "%s is absent after the replacement", name)
	}

	assert.False(t, msg.GetAddress().ProtoReflect().Has(msg.GetAddress().ProtoReflect().Descriptor().Fields().ByName("street")))
	assert.Equal(t, "Renamed", msg.GetName())
	assert.Equal(t, "1", msg.GetId())
}

// TestApplyUpdateMask_ExplicitPathToUnsetFieldClears pins that a path naming
// a field the request leaves unset clears it: the field stays absent, with
// no presence, at any depth.
func TestApplyUpdateMask_ExplicitPathToUnsetFieldClears(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []string
		check func(*testing.T, *testpb.UpdateRequest)
	}{
		{name: "optional scalar", paths: []string{"priority"}, check: func(t *testing.T, r *testpb.UpdateRequest) {
			t.Helper()
			assert.False(t, has(r, "priority"))
		}},
		{name: "message", paths: []string{"options"}, check: func(t *testing.T, r *testpb.UpdateRequest) {
			t.Helper()
			assert.Nil(t, r.GetOptions())
		}},
		{name: "field of an unset message", paths: []string{"options.color"}, check: func(t *testing.T, r *testpb.UpdateRequest) {
			t.Helper()
			assert.Nil(t, r.GetOptions(), "no empty parent is created")
		}},
		{name: "field of a set message", paths: []string{"shipping_address.zip", "shipping_address.street"},
			check: func(t *testing.T, r *testpb.UpdateRequest) {
				t.Helper()
				require.NotNil(t, r.GetShippingAddress())
				assert.Equal(t, "Old", r.GetShippingAddress().GetStreet())
				assert.False(t, has(r.GetShippingAddress(), "zip"))
			}},
		{name: "repeated and map", paths: []string{"tags", "metadata"}, check: func(t *testing.T, r *testpb.UpdateRequest) {
			t.Helper()
			assert.False(t, has(r, "tags"))
			assert.False(t, has(r, "metadata"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &testpb.UpdateRequest{Id: "1", ShippingAddress: &testpb.Address{Street: new("Old")}}
			mask := fieldmask.FromPaths(tt.paths...)
			require.NoError(t, update.Apply(mask, req, update.WithCurrent(storedRequest())))

			for _, path := range tt.paths {
				assert.True(t, mask.Contains(path), "%s stays in the mask so the merge clears it", path)
			}

			tt.check(t, req)
		})
	}
}

// TestApplyUpdateMask_JSONSpelling pins that a mask written in the JSON
// spelling of its fields applies to the same fields as the declared one, in
// every path validation mode, and comes back spelled by declared names.
func TestApplyUpdateMask_JSONSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []update.Option
	}{
		{name: "validation disabled", opts: []update.Option{update.WithPathValidation(update.PathValidationDisabled)}},
		{name: "validation warn", opts: []update.Option{
			update.WithPathValidation(update.PathValidationWarn),
			update.WithPathValidationReporter(func(err *fieldmask.ValidationError) { t.Errorf("unexpected report: %v", err) }),
		}},
		{name: "validation enforced", opts: []update.Option{update.WithPathValidation(update.PathValidationEnforce)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedRequest()
			req.ShippingAddress.Street = new("New")

			mask := fieldmask.FromPaths("shippingAddress.street", "resourceName")
			require.NoError(t, update.Apply(mask, req, append(tt.opts, update.WithCurrent(storedRequest()))...))

			assert.Equal(t, []string{"id", "resource_name", "shipping_address.street"}, mask.ToPaths())
			assert.Equal(t, "New", req.GetShippingAddress().GetStreet())
			assert.Equal(t, "requests/1", req.GetResourceName())
			assert.Equal(t, "1", req.GetId())
			assert.Nil(t, req.GetAddress(), "a field outside the mask is cleared")
		})
	}
}

// TestValidateStrictUpdateMask_JSONSpelling pins that the strict mask
// accepts the JSON spelling and applies the behaviors to the field it names.
func TestValidateStrictUpdateMask_JSONSpelling(t *testing.T) {
	t.Parallel()

	req := storedRequest()
	mask, err := update.ValidateStrict(fieldmask.FromPaths("shippingAddress.street", "createdAt"), req,
		update.WithCurrent(storedRequest()))
	require.NoError(t, err)
	assert.Equal(t, []string{"shipping_address.street"}, mask.ToPaths(), "OUTPUT_ONLY createdAt is dropped")

	req.Code = new("C2")
	_, err = update.ValidateStrict(fieldmask.FromPaths("code"), req, update.WithCurrent(storedRequest()))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)

	_, err = update.ValidateStrict(fieldmask.FromPaths("shippingAddress.nope"), req)
	require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
}

// TestExpandMessagePaths_JSONSpelling pins that ExpandMessagePaths expands
// a message path written in its JSON spelling.
func TestExpandMessagePaths_JSONSpelling(t *testing.T) {
	t.Parallel()

	got := update.ExpandMessagePaths(fieldmask.FromPaths("shippingAddress"), storedRequest())
	assert.Equal(t, []string{"shipping_address.street", "shipping_address.zip"}, got.ToPaths())
}

// TestValidateStrictUpdateMask_EmptyMask pins AIP-134's provided empty mask:
// a no-op, not an error, whose result is an empty mask, with no field
// behavior checked.
func TestValidateStrictUpdateMask_EmptyMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mask    fieldmask.FieldMask
		mutate  func(*testpb.UpdateRequest)
		wantErr error
	}{
		{name: "empty mask", mask: fieldmask.FieldMask{}},
		{name: "empty mask, changed fields are not updated", mask: fieldmask.FieldMask{},
			mutate: func(r *testpb.UpdateRequest) { r.Description, r.Code = new("new"), new("C2") }},
		{name: "empty mask, changed identifier is not checked", mask: fieldmask.FieldMask{},
			mutate: func(r *testpb.UpdateRequest) { r.ResourceName = new("requests/2") }},
		{name: "wildcard with another path", mask: fieldmask.FromPaths("*", "status"), wantErr: update.ErrInvalidUpdateMask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedRequest()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			got, err := update.ValidateStrict(tt.mask, req, update.WithCurrent(storedRequest()))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, got)
		})
	}
}

// TestFromSetFieldsForUpdate_MaskPolicy pins that the implied mask of an
// omitted update_mask leaves out the identity fields and the implied-skip
// paths of the policy, and keeps a set field of WithImmutableFields for
// Apply to compare.
func TestFromSetFieldsForUpdate_MaskPolicy(t *testing.T) {
	t.Parallel()

	policy := []update.Option{
		update.WithImpliedSkipFields("priority", "shipping_address.zip"),
		update.WithImmutableFields("description"),
	}

	req := storedRequest()
	req.ShippingAddress.Zip = new("11000")

	mask := update.FromSetFields(req, policy...)
	assert.False(t, mask.Contains("id"), "identity field")
	assert.False(t, mask.Contains("resource_name"), "IDENTIFIER field")
	assert.False(t, mask.Contains("priority"), "implied-skip field")
	assert.False(t, mask.Contains("shipping_address.zip"), "nested implied-skip field")
	assert.True(t, mask.Contains("shipping_address.street"))
	assert.True(t, mask.Contains("description"), "a set immutable field is compared, not skipped")

	opts := append(append([]update.Option(nil), policy...), update.WithCurrent(storedRequest()))
	require.NoError(t, update.Apply(mask.Clone(), proto.Clone(req), opts...))

	req.Description = new("new")
	err := update.Apply(update.FromSetFields(req, policy...), req, opts...)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

// TestApplyUpdateMask_AIP203Regression pins the AIP-203 behaviors that the
// full replacement and the cleared fields keep.
func TestApplyUpdateMask_AIP203Regression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		paths   []string
		mutate  func(*testpb.UpdateRequest)
		current bool
		want    []string
	}{
		{name: "full replacement checks REQUIRED fields", paths: []string{"*"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Name, r.Address.City = nil, nil }, want: []string{"address.city", "name"}},
		{name: "full replacement compares a set IMMUTABLE field", paths: []string{"*"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Code = new("C2") }, want: []string{"code"}},
		{name: "full replacement without the stored resource rejects a set IMMUTABLE field", paths: []string{"*"},
			want: []string{"code"}},
		{name: "full replacement ignores OUTPUT_ONLY values", paths: []string{"*"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.CreatedAt = new("client") }},
		{name: "cleared IMMUTABLE path is a change", paths: []string{"code"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Code = nil }, want: []string{"code"}},
		{name: "cleared REQUIRED path", paths: []string{"name"}, current: true,
			mutate: func(r *testpb.UpdateRequest) { r.Name = nil }, want: []string{"name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedRequest()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			var opts []update.Option
			if tt.current {
				opts = append(opts, update.WithCurrent(storedRequest()))
			}

			_, strictErr := update.ValidateStrict(fieldmask.FromPaths(tt.paths...), req, opts...)
			err := update.Apply(fieldmask.FromPaths(tt.paths...), req, opts...)

			if tt.want == nil {
				require.NoError(t, err)
				require.NoError(t, strictErr)
				assert.False(t, has(req, "created_at"))

				return
			}

			require.ErrorIs(t, strictErr, update.ErrBehaviorViolation)

			var got []string
			for _, v := range behaviorViolations(t, err) {
				got = append(got, v.Path)
			}

			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

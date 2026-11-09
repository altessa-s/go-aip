// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func TestApplyUpdateMask_EmptyMask(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1", Status: "active"}
	mask := fieldmask.FieldMask{}
	err := update.Apply(mask, msg)
	require.NoError(t, err)
	assert.True(t, mask.IsEmpty(), "nothing to update")
	assert.Equal(t, "1", msg.GetId())
	assert.Equal(t, "active", msg.GetStatus(), "an empty mask leaves the message untouched")
}

// RFC edge case 1: Scalar field in mask, not set in request.
// update_mask: ["name", "description"], request: {name: "John"}
// Result: name = "John", description absent (cleared, no presence).
func TestApplyUpdateMask_ScalarInMaskNotSet(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("name", "description")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "John", msg.GetName())
	// description is cleared: absent, not a default with presence.
	assert.False(t, msg.ProtoReflect().Has(
		msg.ProtoReflect().Descriptor().Fields().ByName("description"),
	))
	assert.Equal(t, "", msg.GetDescription())
	// The identity field addresses the resource and is kept.
	assert.Equal(t, "1", msg.GetId())
}

// RFC edge case 2: Optional field (presence semantics) — clearing.
// update_mask: ["description"], request: {} (description not set)
// Result: description stays absent, so the update clears it.
func TestApplyUpdateMask_OptionalFieldClearing(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("description")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	descFD := msg.ProtoReflect().Descriptor().Fields().ByName("description")
	assert.False(t, msg.ProtoReflect().Has(descFD), "a cleared optional field has no presence")
	assert.Equal(t, "", msg.GetDescription())
}

// RFC edge case 3: Nested message not set, but sub-field in mask.
// update_mask: ["options.color"], request: {id: "1"}
// Result: options stays unset; no empty Options{} is created.
func TestApplyUpdateMask_NestedMessageNotSet(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("options.color")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Nil(t, msg.GetOptions(), "an unset message gets no presence")
}

// Variant: REQUIRED nested message not set, with sub-field in mask → error.
// The mask names address.city, a REQUIRED field the request leaves empty.
func TestApplyUpdateMask_RequiredNestedMessageNotSet(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("address.city")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "address.city", behaviorErr.Violations[0].Path)
}

// RFC edge case 4: Repeated (list) field in mask, not set.
// update_mask: ["tags"], request: {}
// Result: tags stays as empty list (already default).
func TestApplyUpdateMask_RepeatedFieldInMaskNotSet(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("tags")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Empty(t, msg.GetTags())
}

// RFC edge case 5: Map field in mask, not set.
// update_mask: ["metadata"], request: {}
// Result: metadata stays as empty map (already default).
func TestApplyUpdateMask_MapFieldInMaskNotSet(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("metadata")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Empty(t, msg.GetMetadata())
}

// RFC edge case 6: Field is set AND in mask.
// update_mask: ["name"], request: {name: "John"}
// Result: name = "John" — kept as-is.
func TestApplyUpdateMask_FieldSetAndInMask(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("name")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "John", msg.GetName())
	// id not in mask — kept as the address of the resource.
	assert.Equal(t, "1", msg.GetId())
}

// RFC edge case 7: REQUIRED field in mask but NOT set (BLOCKED).
// update_mask: ["name", "description"], request: {description: "new desc"}
// name has REQUIRED behavior but is not set → error.
func TestApplyUpdateMask_RequiredFieldNotSet(t *testing.T) {
	desc := "new desc"
	msg := &testpb.UpdateRequest{
		Id:          "1",
		Description: &desc,
	}

	mask := fieldmask.FromPaths("name", "description")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "name", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "required")

	// Message should NOT be modified on error (fail-fast).
	assert.Equal(t, "1", msg.GetId(), "message should not be modified on validation error")
}

// RFC edge case 8: REQUIRED field NOT in mask (OK).
// update_mask: ["description"], request: {description: "new desc"}
// name has REQUIRED behavior but is NOT in mask — not affected.
func TestApplyUpdateMask_RequiredFieldNotInMask(t *testing.T) {
	desc := "new desc"
	msg := &testpb.UpdateRequest{
		Id:          "1",
		Description: &desc,
	}

	mask := fieldmask.FromPaths("description")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "new desc", msg.GetDescription())
}

// RFC edge case 9: REQUIRED field in mask AND set (OK).
// update_mask: ["name", "description"], request: {name: "New Name", description: "new desc"}
func TestApplyUpdateMask_RequiredFieldSetInMask(t *testing.T) {
	name := "New Name"
	desc := "new desc"
	msg := &testpb.UpdateRequest{
		Id:          "1",
		Name:        &name,
		Description: &desc,
	}

	mask := fieldmask.FromPaths("name", "description")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "New Name", msg.GetName())
	assert.Equal(t, "new desc", msg.GetDescription())
}

// RFC edge case 10: IMMUTABLE field in mask (BLOCKED).
// update_mask: ["code"], request: {code: "new_code"}
func TestApplyUpdateMask_ImmutableField(t *testing.T) {
	code := "new_code"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Code: &code,
	}

	mask := fieldmask.FromPaths("code")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "code", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "immutable")
}

// RFC edge case 11: OUTPUT_ONLY field in mask (silently ignored).
// update_mask: ["name", "created_at"], request: {name: "John", created_at: "2026-01-01"}
func TestApplyUpdateMask_OutputOnlyField(t *testing.T) {
	name := "John"
	ts := "2026-01-01"
	msg := &testpb.UpdateRequest{
		Id:        "1",
		Name:      &name,
		CreatedAt: &ts,
	}

	mask := fieldmask.FromPaths("name", "created_at")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "John", msg.GetName())
	// created_at should be cleared (removed from mask, then cleared by filter).
	assert.Equal(t, "", msg.GetCreatedAt())
}

// RFC edge case 12: Nested REQUIRED field.
// update_mask: ["address.city", "address.street"], request: {address: {street: "Main St"}}
// address.city is REQUIRED but not set → error.
func TestApplyUpdateMask_NestedRequiredField(t *testing.T) {
	street := "Main St"
	msg := &testpb.UpdateRequest{
		Id: "1",
		Address: &testpb.Address{
			Street: &street,
		},
	}

	mask := fieldmask.FromPaths("address.city", "address.street")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "address.city", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "required")
}

func TestApplyUpdateMask_NestedRequiredFieldSet(t *testing.T) {
	city := "Moscow"
	street := "Main St"
	msg := &testpb.UpdateRequest{
		Id: "1",
		Address: &testpb.Address{
			City:   &city,
			Street: &street,
		},
	}

	mask := fieldmask.FromPaths("address.city", "address.street")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "Moscow", msg.GetAddress().GetCity())
	assert.Equal(t, "Main St", msg.GetAddress().GetStreet())
}

// Nested REQUIRED field whose unannotated parent message is absent from the request.
// update_mask: ["shipping_address.city"], request: {id: "1"}
// shipping_address is not REQUIRED and not set; city is REQUIRED → error.
func TestApplyUpdateMask_NestedRequiredFieldParentAbsent(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("shipping_address.city")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "shipping_address.city", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "required")
}

func TestApplyUpdateMask_MultipleViolations(t *testing.T) {
	code := "abc"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Code: &code,
	}

	// name is REQUIRED (not set), code is IMMUTABLE
	mask := fieldmask.FromPaths("name", "code")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	assert.GreaterOrEqual(t, len(behaviorErr.Violations), 2)
}

func TestApplyUpdateMask_UnionWithProtectedField(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	// Simulate handler pattern: fm.Union(FromPaths("id")).Apply(in).
	// id is an identity field of the default policy: unchanged, it is ignored.
	mask := fieldmask.FromPaths("name").Union(fieldmask.FromPaths("id"))
	err := update.Apply(mask, msg, update.WithCurrent(&testpb.UpdateRequest{Id: "1"}))
	require.NoError(t, err)

	assert.Equal(t, "1", msg.GetId())
	assert.Equal(t, "John", msg.GetName())
}

func TestApplyUpdateMask_SetDefaultForScalarNoPresence(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id:     "1",
		Status: "active",
	}

	// status is proto3 scalar (no presence) — if in mask but not "set",
	// default is already "". The field will be cleared by filter then set to default.
	mask := fieldmask.FromPaths("status")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	// status was "active" and IS in the mask, so it should be kept.
	assert.Equal(t, "active", msg.GetStatus())
}

func TestApplyUpdateMask_NestedOptionsClearing(t *testing.T) {
	color := "red"
	msg := &testpb.UpdateRequest{
		Id: "1",
		Options: &testpb.Options{
			Color: &color,
		},
	}

	// size in the mask but not set → cleared (no presence).
	mask := fieldmask.FromPaths("options.color", "options.size")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	require.NotNil(t, msg.GetOptions())
	assert.Equal(t, "red", msg.GetOptions().GetColor())

	sizeFD := msg.GetOptions().ProtoReflect().Descriptor().Fields().ByName("size")
	assert.False(t, msg.GetOptions().ProtoReflect().Has(sizeFD))
	assert.Equal(t, int32(0), msg.GetOptions().GetSize())
}

func TestApplyUpdateMask_PriorityDefault(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("priority")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	prioFD := msg.ProtoReflect().Descriptor().Fields().ByName("priority")
	assert.False(t, msg.ProtoReflect().Has(prioFD))
	assert.Equal(t, int32(0), msg.GetPriority())
}

func TestApplyUpdateMask_ImmutableFieldNotSet(t *testing.T) {
	// IMMUTABLE field in mask but not set → should also be an error.
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("code")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "immutable")
}

func TestApplyUpdateMask_OutputOnlyRemovedFromMask(t *testing.T) {
	ts := "2026-01-01"
	msg := &testpb.UpdateRequest{
		CreatedAt: &ts,
	}

	mask := fieldmask.FromPaths("created_at")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	// created_at was the only path and is dropped, so the update changes
	// nothing: the mask comes back empty and msg is left as sent.
	assert.True(t, mask.IsEmpty())
	assert.Equal(t, ts, msg.GetCreatedAt())
}

func TestApplyUpdateMask_MessageNotMutatedOnError(t *testing.T) {
	name := "Original"
	desc := "Original desc"
	msg := &testpb.UpdateRequest{
		Id:          "1",
		Name:        &name,
		Description: &desc,
	}

	original := proto.Clone(msg).(*testpb.UpdateRequest)

	// code is IMMUTABLE → error
	mask := fieldmask.FromPaths("name", "description", "code")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	// Message should be unchanged.
	assert.True(t, proto.Equal(original, msg), "message should not be modified on validation error")
}

func TestApplyUpdateMask_FieldNotInSchema(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	// With validation explicitly disabled, "nonexistent" is silently ignored
	// (no field descriptor found).
	mask := fieldmask.FromPaths("name", "nonexistent")
	err := update.Apply(mask, msg, update.WithPathValidation(update.PathValidationDisabled))
	require.NoError(t, err)

	assert.Equal(t, "John", msg.GetName())
}

// AIP-161: with WithPathValidation an unknown root path is rejected, not ignored.
func TestApplyUpdateMask_WithPathValidation_RejectsUnknownRootPath(t *testing.T) {
	desc := "hello"
	msg := &testpb.UpdateRequest{Id: "1", Description: &desc}

	mask := fieldmask.FromPaths("descriptionggg")
	err := update.Apply(mask, msg, update.WithPathValidation(update.PathValidationEnforce))

	var validationErr *fieldmask.ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, "descriptionggg", validationErr.Path)
}

// AIP-161: an invalid nested leaf is rejected before any mutation, so the parent
// struct is preserved instead of being silently cleared (data-loss regression).
func TestApplyUpdateMask_WithPathValidation_RejectsNestedAndPreservesData(t *testing.T) {
	color := "red"
	size := int32(42)
	msg := &testpb.UpdateRequest{
		Id:      "1",
		Options: &testpb.Options{Color: &color, Size: &size},
	}

	mask := fieldmask.FromPaths("options.colorrr")
	err := update.Apply(mask, msg, update.WithPathValidation(update.PathValidationEnforce))

	var validationErr *fieldmask.ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, "options.colorrr", validationErr.Path)

	// No data loss: the whole Options struct is untouched.
	require.NotNil(t, msg.GetOptions())
	assert.Equal(t, "red", msg.GetOptions().GetColor())
	assert.Equal(t, int32(42), msg.GetOptions().GetSize())
}

// With WithPathValidation a valid nested path still applies without error.
func TestApplyUpdateMask_WithPathValidation_ValidPathApplies(t *testing.T) {
	color := "red"
	msg := &testpb.UpdateRequest{
		Id:      "1",
		Options: &testpb.Options{Color: &color},
	}

	mask := fieldmask.FromPaths("options.color")
	err := update.Apply(mask, msg, update.WithPathValidation(update.PathValidationEnforce))
	require.NoError(t, err)
	assert.Equal(t, "red", msg.GetOptions().GetColor())
}

// PathValidationWarn reports the bad path through the reporter but does not fail:
// the mask is still applied, preserving the Disabled behavior.
func TestApplyUpdateMask_WithPathValidation_WarnReportsAndApplies(t *testing.T) {
	desc := "hello"
	msg := &testpb.UpdateRequest{Id: "1", Description: &desc}

	var reported []string

	mask := fieldmask.FromPaths("descriptionggg")
	err := update.Apply(mask, msg,
		update.WithPathValidation(update.PathValidationWarn),
		update.WithPathValidationReporter(func(ve *fieldmask.ValidationError) { reported = append(reported, ve.Path) }),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"descriptionggg"}, reported)
}

// PathValidationWarn reports every schema-invalid path, not just the first
// (collect-all), unlike PathValidationEnforce which fails fast on the first.
func TestApplyUpdateMask_WithPathValidation_WarnReportsAllInvalidPaths(t *testing.T) {
	desc := "hello"
	color := "red"
	msg := &testpb.UpdateRequest{
		Id:          "1",
		Description: &desc,
		Options:     &testpb.Options{Color: &color},
	}

	var reported []string

	mask := fieldmask.FromPaths("descriptionggg", "optionsxxx", "options.colorrr")
	err := update.Apply(mask, msg,
		update.WithPathValidation(update.PathValidationWarn),
		update.WithPathValidationReporter(func(ve *fieldmask.ValidationError) { reported = append(reported, ve.Path) }),
	)

	require.NoError(t, err)
	// ToPaths is sorted, so the reported order is deterministic.
	assert.Equal(t, []string{"descriptionggg", "options.colorrr", "optionsxxx"}, reported)
}

// PathValidationWarn with no reporter is a silent no-op: no panic, no error.
func TestApplyUpdateMask_WithPathValidation_WarnWithoutReporter(t *testing.T) {
	desc := "hello"
	msg := &testpb.UpdateRequest{Id: "1", Description: &desc}

	mask := fieldmask.FromPaths("descriptionggg")
	err := update.Apply(mask, msg, update.WithPathValidation(update.PathValidationWarn))
	require.NoError(t, err)
}

// The default (no option) enforces path validation (AIP-161): a schema-invalid
// path is rejected before any mutation.
func TestApplyUpdateMask_WithPathValidation_EnforcedByDefault(t *testing.T) {
	desc := "hello"
	msg := &testpb.UpdateRequest{Id: "1", Description: &desc}

	mask := fieldmask.FromPaths("descriptionggg")
	err := update.Apply(mask, msg)

	var ve *fieldmask.ValidationError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, "descriptionggg", ve.Path)
	require.Equal(t, "hello", msg.GetDescription())
}

// AIP-203 IDENTIFIER: field in mask is rejected like IMMUTABLE.
// update_mask: ["resource_name"], request: {resource_name: "items/123"}
func TestApplyUpdateMask_IdentifierFieldInMask(t *testing.T) {
	resourceName := "items/123"
	msg := &testpb.UpdateRequest{
		Id:           "1",
		ResourceName: &resourceName,
	}

	mask := fieldmask.FromPaths("resource_name")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "resource_name", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "identifier")
}

// AIP-203 IDENTIFIER not in mask: identifier is set to route the update,
// other fields are updated, identifier is preserved.
func TestApplyUpdateMask_IdentifierNotInMask(t *testing.T) {
	resourceName := "items/123"
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:           "1",
		ResourceName: &resourceName,
		Name:         &name,
	}

	mask := fieldmask.FromPaths("name")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.Equal(t, "items/123", msg.GetResourceName(), "identifier preserved when not in mask")
	assert.Equal(t, "John", msg.GetName())
}

// IDENTIFIER in mask but not set in the request still violates: matching
// IMMUTABLE semantics so callers can't smuggle identifier mutation through
// an empty value.
func TestApplyUpdateMask_IdentifierFieldInMaskNotSet(t *testing.T) {
	msg := &testpb.UpdateRequest{Id: "1"}

	mask := fieldmask.FromPaths("resource_name")
	err := update.Apply(mask, msg)
	require.Error(t, err)

	var behaviorErr *update.BehaviorError

	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.ErrorAs(t, err, &behaviorErr)
	require.Len(t, behaviorErr.Violations, 1)
	assert.Equal(t, "resource_name", behaviorErr.Violations[0].Path)
	assert.Contains(t, behaviorErr.Violations[0].Reason, "identifier")
}

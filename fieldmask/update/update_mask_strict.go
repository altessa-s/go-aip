// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
)

// StrictError describes invalid paths in a strict update mask.
type StrictError struct {
	// Cause preserves an underlying schema or resource error.
	Cause      error
	Violations []FieldViolation
}

// Error lists the violations.
func (e *StrictError) Error() string {
	switch {
	case len(e.Violations) == 0:
		return "invalid update_mask"
	case e.Violations[0].Path == "": // a mask-level violation names no path
		return "invalid update_mask: " + e.Violations[0].Reason
	}

	return fmt.Sprintf("invalid update_mask path %q: %s", e.Violations[0].Path, e.Violations[0].Reason)
}

// ValidateStrict validates the update mask msk of resource and
// returns the cleaned mask the update applies, ready for
// [Apply]. Pass the stored resource with [WithCurrent]
// and the mask policy options; path validation is always
// enforced, so [WithPathValidation] does not apply.
//
// msk keeps the presence of the update_mask (AIP-134), as
// [fieldmask.FromProtoFieldMask] builds it. A nil msk is an omitted update_mask: it is
// validated as its implied mask, [FromSetFields] of resource under
// the options. A non-nil empty msk is an update_mask sent with no
// paths: a no-op, returned as an empty mask without checking any field
// behavior; only a missing resource or a [WithCurrent] message that is
// invalid or of another type is still an error, as for [Spec.Plan].
// Whatever the mask, an empty result ([fieldmask.FieldMask.IsEmpty]) means the update
// changes nothing, mirroring [Plan.Empty]: every path was dropped, or
// none was sent.
//
// Every path is resolved against resource's schema, so a segment may use any
// spelling of its field ([fieldmask.FieldMask.Resolve]); the returned mask spells it by
// its declared name. An unknown path, an indexed list path ("aliases.0") and a
// sub-path into an atomic message are violations. Every path is checked on
// its own, one that another path covers once both are resolved included (see
// [Apply]).
//
// The AIP-203 rules and the mask policy are those of Apply,
// without mutating resource: OUTPUT_ONLY paths, at any depth, are dropped
// from the returned mask and never reported; an IMMUTABLE or IDENTIFIER path
// (a top-level field of [WithImmutableFields] or [WithIdentityFields]
// included) whose value equals the stored one is dropped, and a changed one
// — or any such path without [WithCurrent] — is a violation; a path naming a
// REQUIRED field left empty is a violation; and a singular message named
// whole that holds a server-managed field is expanded to its other fields so
// its stored server-managed values survive. AIP-134's "*" wildcard alone is a
// full replacement: it expands to every field but the OUTPUT_ONLY ones,
// keeps the stored IMMUTABLE and IDENTIFIER values the resource leaves unset,
// compares the ones it sets, and checks the REQUIRED fields of resource. "*"
// with other paths is a violation.
//
// Errors are *StrictError, matching [ErrInvalidUpdateMask] and, for
// field behavior violations, [ErrBehaviorViolation].
func ValidateStrict(msk fieldmask.FieldMask, resource proto.Message, opts ...Option) (fieldmask.FieldMask, error) {
	o := newOptions(opts...)
	policy := o

	if resource == nil {
		return nil, &StrictError{Cause: ErrMessageMismatch, Violations: []FieldViolation{{Path: policy.updateMaskFieldName,
			Reason: "resource is missing"}}}
	}

	u, root, err := newApplyCheck(resource, o.current, policy)
	if err != nil {
		return nil, strictError(err, policy.updateMaskFieldName)
	}

	switch {
	case msk == nil:
		return ValidateStrict(impliedUpdateMask(resource, o), resource, opts...)
	case len(msk) == 0:
		return fieldmask.FieldMask{}, nil // a provided empty update_mask is a no-op (AIP-134).
	}

	descriptor := resource.ProtoReflect().Descriptor()

	all, err := msk.SelectsAll()
	if err != nil {
		return nil, &StrictError{Cause: err, Violations: []FieldViolation{{Path: maskpath.Wildcard, Reason: maskpath.ReasonWildcardNotAlone}}}
	}

	if all {
		work := u.replaceAll(descriptor, root)
		u.checkOneofSwitches(descriptor, root, work, nil)

		return strictResult(work, u, descriptor, policy.updateMaskFieldName)
	}

	work, covered, err := resolveUpdatePaths(msk.ToPaths(), descriptor, policy, &options{pathValidation: PathValidationEnforce})
	if err != nil {
		return nil, strictError(err, policy.updateMaskFieldName)
	}

	u.checkCovering(work, covered, descriptor, root)
	u.checkOneofSwitches(descriptor, root, work, covered)

	return strictResult(work, u, descriptor, policy.updateMaskFieldName)
}

// strictResult returns work, or the refusal or field behavior violations u
// recorded.
func strictResult(work fieldmask.FieldMask, u *updateCheck, md protoreflect.MessageDescriptor, maskField string) (fieldmask.FieldMask, error) {
	if err := u.maskRefusal(work, md); err != nil {
		return nil, strictError(err, maskField)
	}

	if len(u.violations) != 0 {
		return nil, &StrictError{Violations: u.violations}
	}

	return work, nil
}

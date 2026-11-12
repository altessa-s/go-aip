// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskpath"
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

// ValidateStrict validates an update mask under the rules of [Apply] without
// mutating resource, and returns the effective write mask. It enforces grammar,
// schema, structural restrictions and limits regardless of [WithPathValidation].
// OUTPUT_ONLY paths are dropped. With [WithCurrent], equal IMMUTABLE and
// IDENTIFIER values are ignored, while changes (including selected clears) and
// missing REQUIRED values are violations.
//
// Without [WithCurrent], protected values remain in resource and their paths
// are omitted from the effective mask; comparison with the stored resource is
// the caller's duty. Use [Prepare] for two-stage normalization with retained
// clear intent, completed by [Spec.Plan] or [Patch.Run]. Whole collections and
// atomic values with protected descendants are deferred as described by [Apply].
//
// A nil mask implies populated fields, a provided empty mask is a no-op, and
// "*" alone is a full replacement. Without stored state, an empty result is an
// interim effective mask: it does not permit skipping completion of a [Prepare]
// plan. Only a successfully completed empty mask means no merge is needed. Errors are
// *[StrictError], matching [ErrInvalidUpdateMask] and their underlying cause;
// field behavior violations also match [ErrBehaviorViolation].
// A nil or invalid resource matches [ErrMessageMismatch].
func ValidateStrict(msk fieldmask.FieldMask, resource proto.Message, opts ...Option) (fieldmask.FieldMask, error) {
	o := newOptions(opts...)
	policy := o

	if resource == nil {
		return nil, &StrictError{Cause: ErrMessageMismatch, Violations: []FieldViolation{{Path: policy.updateMaskFieldName,
			Reason: "resource is missing"}}}
	}

	resource, err := cloneMessage(resource)
	if err != nil {
		return nil, strictError(err, policy.updateMaskFieldName)
	}

	u, root, err := newApplyCheck(resource, o.current, policy)
	if err != nil {
		return nil, strictError(err, policy.updateMaskFieldName)
	}

	switch {
	case msk == nil:
		u.orderMode = referenceImplied
		msk = u.impliedSelection(root)
		u.checkImplied(resource.ProtoReflect().Descriptor(), root)
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

		return strictResult(work, u, descriptor, policy.updateMaskFieldName)
	}

	work, covered, err := resolveUpdatePaths(msk.ToPaths(), descriptor, policy, &options{pathValidation: PathValidationEnforce})
	if err != nil {
		return nil, strictError(err, policy.updateMaskFieldName)
	}

	u.checkCovering(work, covered, descriptor, root)

	return strictResult(work, u, descriptor, policy.updateMaskFieldName)
}

// strictResult returns work, or the refusal or field behavior violations u
// recorded.
func strictResult(work fieldmask.FieldMask, u *updateCheck, md protoreflect.MessageDescriptor, maskField string) (fieldmask.FieldMask, error) {
	if err := u.maskRefusal(work, md); err != nil {
		return nil, strictError(err, maskField)
	}

	violations, err := u.orderedViolations(md)
	if err != nil {
		return nil, strictError(err, maskField)
	}

	if len(violations) > 0 {
		return nil, &StrictError{Violations: violations}
	}

	return work, nil
}

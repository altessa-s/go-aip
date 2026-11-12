// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// planViolationError is the updateMask field error of the field behavior
// violation v, located at the explicit path that covers it.
func planViolationError(v FieldViolation, explicit []maskPath) error {
	switch {
	case v.Behavior == annotations.FieldBehavior_REQUIRED:
		return planPathError(ErrBehaviorViolation, v.Path, explicit, fmt.Sprintf("path %q is required and cannot be empty", v.Path))
	case v.Reason == behavior.ReasonImmutableChanged || v.Reason == behavior.ReasonIdentifierChanged:
		return planPathError(&changedError{path: v.Path}, v.Path, explicit, fmt.Sprintf("path %q cannot be changed", v.Path))
	default:
		// The remaining refusal is the conservative stateless oneof guard;
		// preserve the base guard's explanation and sentinel-only cause.
		return planPathError(ErrProtectedField, v.Path, explicit, v.Reason)
	}
}

// planRefusalError is the field error of err, a refusal of the mask itself
// ([updateCheck.maskRefusal]), located at the explicit mask path that covers
// its path: a *fieldmask.ValidationError classified by its Cause, or
// [fieldmask.ErrInvalidPath] without one. Any other refusal (a request value
// nesting more messages than the REQUIRED walk checks) is located on the
// bare mask field and classified by err itself.
func planRefusalError(err error, explicit []maskPath) error {
	if count, ok := errors.AsType[*elementCountError](err); ok {
		return planPathError(ErrElementCountMismatch, count.Path, explicit, count.Reason)
	}

	ve, ok := errors.AsType[*fieldmask.ValidationError](err)
	if !ok {
		return indexedFieldError(err, -1, "", "", err.Error())
	}

	cause := ve.Cause
	if cause == nil {
		cause = fieldmask.ErrInvalidPath
	}

	return planPathError(cause, ve.Path, explicit, ve.Reason)
}

// orderImpliedBoundaryRefusal applies the Spec's base implied-mask order to
// violations already found by the boundary. ApplyMask checks its populated
// tree in a different order; taking its first violation would change Spec's
// public refusal merely because a request passed through Prepare. The boundary
// still decides acceptance, and reference-only violations are never added.
func (s *Spec) orderImpliedBoundaryRefusal(err error, pending *DeferredPlan, input, current proto.Message) error {
	be, ok := errors.AsType[*BehaviorError](err)
	if pending.raw != nil || !ok || len(be.Violations) < 2 {
		return err
	}

	u, _, checkErr := newUpdateCheck(input, current, s.opts)
	if checkErr != nil {
		return checkErr
	}

	u.top = s.topBehavior
	u.orderMode = referenceSpecImplied
	u.orderSkip = slices.Concat(s.skip, s.opts.impliedSkipFields)
	u.violations = slices.Clone(be.Violations)

	violations, orderErr := u.orderedViolations(s.desc)
	if orderErr != nil {
		return orderErr
	}

	return &BehaviorError{Violations: violations}
}

// completionError translates a refusal under the normalization policy into
// the spec's base error construction. A different boundary policy may refuse
// input the spec accepts; it still must not introduce an aggregate plan error.
func (s *Spec) completionError(err error, raw *fieldmaskpb.FieldMask, boundary *options) error {
	var explicit []maskPath

	for i, path := range raw.GetPaths() {
		if segs, reason := resolveUpdateSegments(s.desc, path, boundary); reason == "" {
			explicit = append(explicit, maskPath{index: i, raw: path, segs: segs})
		}
	}

	if be, ok := errors.AsType[*BehaviorError](err); ok && len(be.Violations) > 0 {
		return s.fieldError(planViolationError(be.Violations[0], explicit))
	}

	if se, ok := errors.AsType[*StrictError](err); ok {
		if len(se.Violations) > 0 && se.Violations[0].Behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return s.fieldError(planViolationError(se.Violations[0], explicit))
		}

		if se.Cause != nil {
			err = se.Cause
		}
	}

	if errors.Is(err, ErrMessageMismatch) {
		return err
	}

	return s.fieldError(planRefusalError(err, explicit))
}

// changedError is the cause of a plan refusal for an IMMUTABLE or IDENTIFIER
// value that differs from the stored one: [Patch] reports it on the resource
// field at path. It matches [ErrProtectedField].
type changedError struct{ path string }

func (e *changedError) Error() string {
	return fmt.Sprintf("fieldmask: field %q differs from its stored value", e.path)
}

func (e *changedError) Unwrap() error { return ErrProtectedField }

// MaskPathError locates a refusal of [Spec.Plan] or [Patch.Run] in the
// update mask, so a caller can build the AIP-193 field violation of the
// offending path. The [coreerrs.FieldError] these
// return holds it in its Err chain (errors.AsType finds it) and names the
// same path in its Field: the update-mask field followed by ".paths[Index]",
// or the bare update-mask field when Index is -1.
type MaskPathError struct {
	// Index is the position of the offending path in the update mask, -1
	// when no raw path covers the refusal (including an implied or "*" mask).
	Index int
	// Path is the offending mask path as the request spelled it, "" when
	// Index is -1.
	Path string
	// Field is the resource field the refusal concerns, a path of declared
	// names through the map keys, list indexes or "*" the update check
	// reports ("labels.k.display_name", "aliases.*.id"); "" when the path
	// names no field (an unknown or malformed path).
	Field string
	// Err classifies the refusal: [fieldmask.ErrInvalidPath], [fieldmask.ErrWildcardNotAlone],
	// [ErrProtectedField], [ErrBehaviorViolation] or [ErrElementCountMismatch].
	Err error

	reason string
}

// Error returns the reason of the refusal.
func (e *MaskPathError) Error() string {
	if e.Index < 0 {
		return "fieldmask: update mask: " + e.reason
	}

	return fmt.Sprintf("fieldmask: update mask path %d %q: %s", e.Index, e.Path, e.reason)
}

// Unwrap returns the classification of the refusal.
func (e *MaskPathError) Unwrap() error { return e.Err }

// maskPathField is the request field of the mask path at index of the mask
// field name: name.paths[index], or name itself for index -1.
func maskPathField(name string, index int) string {
	if index < 0 {
		return name
	}

	return name + ".paths[" + strconv.Itoa(index) + "]"
}

// indexedFieldError is the field error of the mask path raw at index i
// (-1 for none) refused with reason; field is the resource field concerned.
func indexedFieldError(cause error, i int, raw, field, reason string) error {
	mp := &MaskPathError{Index: i, Path: raw, Field: field, Err: cause, reason: reason}

	return &coreerrs.FieldError{Field: maskPathField(DefaultUpdateMaskField, i), Reason: reason, Err: mp}
}

// planPathError is the field error of a refusal concerning the resource
// field path, located at the explicit mask path that covers it (see
// [maskIndex]).
func planPathError(cause error, path string, explicit []maskPath, reason string) error {
	i, raw := maskIndex(explicit, path)

	return indexedFieldError(cause, i, raw, path, reason)
}

// locate turns err, a *[MaskPathError] not yet located, into the field error
// of the explicit mask path that covers the resource field path; any other
// error is returned as is.
func locate(err error, path string, explicit []maskPath) error {
	mp, ok := errors.AsType[*MaskPathError](err)
	if !ok {
		return err
	}

	return planPathError(mp.Err, path, explicit, mp.reason)
}

// maskIndex returns the index and spelling of the explicit mask path that
// covers the resource field path (declared names, element keys, indexes or
// "*" included): the longest one whose segments prefix those of path, or that
// path prefixes (a list whose elements a "*" sub-path addresses), "*"
// matching any key or index; the first such path on a tie. Failing that, the
// path updating a sibling of path in place covers it — an IDENTIFIER the
// mask does not name, compared in the message or element a sub-path such as
// "profile.display_name" or "aliases.*.display_name" updates. It returns -1
// and "" when none covers it (an implied or "*" mask) and for an empty path,
// a refusal of the whole mask (its path count, say). A "*" of path, the
// elements a "*" sub-path addresses, is covered by a "*" of an explicit path
// only, never by a key path naming another entry.
func maskIndex(explicit []maskPath, path string) (int, string) {
	if path == "" {
		return -1, "" // a refusal of the whole mask
	}

	segs := maskpath.Split(path)
	best, bestLen, raw := -1, -1, ""
	sibling, siblingLen, siblingRaw := -1, 0, ""

	for _, mp := range explicit {
		k := matchedSegments(mp.segs, segs)

		switch {
		case k == min(len(mp.segs), len(segs)):
			if len(mp.segs) > bestLen {
				best, bestLen, raw = mp.index, len(mp.segs), mp.raw
			}
		case k == len(mp.segs)-1 && k > siblingLen:
			sibling, siblingLen, siblingRaw = mp.index, k, mp.raw
		}
	}

	if best < 0 {
		return sibling, siblingRaw
	}

	return best, raw
}

// matchedSegments returns how many leading segments of a, an explicit mask
// path, and b, the path of a refusal, match: a "*" of a matches any key or
// index of b, but a "*" of b (a refusal of the elements a "*" addresses)
// matches only a "*" of a, never a concrete key, which names another entry.
func matchedSegments(a, b []string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] && a[i] != maskpath.Wildcard {
			return i
		}
	}

	return n
}

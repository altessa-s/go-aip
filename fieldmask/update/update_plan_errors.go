// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/behavior"

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
		return planPathError(ErrProtectedField, v.Path, explicit, fmt.Sprintf("path %q cannot be updated", v.Path))
	}
}

// planRefusalError is the field error of err, a refusal of the mask itself
// ([updateCheck.maskRefusal]), located at the explicit mask path that covers
// its path: a *fieldmask.ValidationError classified by its Cause, or
// [fieldmask.ErrInvalidPath] without one.
func planRefusalError(err error, explicit []maskPath) error {
	ve, ok := errors.AsType[*fieldmask.ValidationError](err)
	if !ok {
		return err
	}

	cause := ve.Cause
	if cause == nil {
		cause = fieldmask.ErrInvalidPath
	}

	return planPathError(cause, ve.Path, explicit, ve.Reason)
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
	// when the refusal comes from the implied or "*" mask.
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
// and "" when none covers it (an implied or "*" mask).
func maskIndex(explicit []maskPath, path string) (int, string) {
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

// matchedSegments returns how many leading segments of a and b match, "*"
// matching any key or index.
func matchedSegments(a, b []string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] && a[i] != maskpath.Wildcard && b[i] != maskpath.Wildcard {
			return i
		}
	}

	return n
}

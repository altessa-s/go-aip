// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"

	"google.golang.org/genproto/googleapis/api/annotations"

	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
)

// Sentinel errors classify update failures independently of diagnostic text.
// A path error matches [github.com/altessa-s/go-aip/fieldmask.ErrInvalidPath].
var (
	// ErrBehaviorViolation reports a rejected field behavior.
	ErrBehaviorViolation = errors.New("fieldmask: field behavior violation")
	// ErrInvalidUpdateMask reports an invalid strict update mask.
	ErrInvalidUpdateMask = errors.New("fieldmask: invalid update mask")
	// ErrProtectedField reports an update that would modify protected fields.
	ErrProtectedField = errors.New("fieldmask: protected field")
	// ErrAllowedPathsAtCall reports that WithAllowedPaths was passed to ResolveMask or ValidateResolved.
	ErrAllowedPathsAtCall = errors.New("fieldmask: allowed paths must be configured in NewSpec")
)

// Unwrap classifies an annotation violation; Violations carries its paths and behaviors.
func (e *BehaviorError) Unwrap() error { return ErrBehaviorViolation }

// Unwrap preserves the validation cause as well as the strict-mask classification.
func (e *StrictError) Unwrap() []error {
	result := coreslices.AppendNonNil([]error{ErrInvalidUpdateMask}, e.Cause)
	for _, v := range e.Violations {
		if v.Behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return append(result, ErrBehaviorViolation)
		}
	}

	return result
}

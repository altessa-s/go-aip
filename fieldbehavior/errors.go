// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// ErrMaxDepthExceeded is returned when [Strip] traverses more nested levels
// than [WithMaxDepth] permits. Protocol Buffers schemas may not contain
// cycles, so hitting the limit indicates a malformed or fuzzed descriptor.
var ErrMaxDepthExceeded = errors.New("fieldbehavior: max traversal depth exceeded")

// ErrCurrentMismatch is returned by [Strip] when the [WithCurrent] message is
// invalid or not of the payload's message type.
var ErrCurrentMismatch = errors.New("fieldbehavior: current message is invalid or of another type")

// BehaviorViolation describes a single field a request cannot carry: a
// populated field [WithStrict] reports instead of clearing, an IMMUTABLE or
// IDENTIFIER value an update would change, a REQUIRED field
// [ValidateRequired] finds absent or empty, or a value that does not match
// the AIP-202 format of its field ([WithFieldFormats]; Behavior is then
// FIELD_BEHAVIOR_UNSPECIFIED, Format is the format name and Reason
// "value <reason> (<format>)"). It aliases [behavior.Violation] so
// the gRPC interceptor can render fieldbehavior and fieldmask violations
// through a single io.altessa.badrequest.v1.BadRequest FieldViolation mapping.
// Path is always set, Behavior unless the violation is a format one, and
// Format only then; Reason explains the rejection.
type BehaviorViolation = behavior.Violation

// BehaviorViolationError aggregates the violations of one [Strip] or
// [ValidateRequired] call. The message is left untouched when this error is
// returned. It maps to INVALID_ARGUMENT.
type BehaviorViolationError struct {
	Violations []BehaviorViolation
}

// Error lists the violations.
func (e *BehaviorViolationError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "fieldbehavior: %d violation(s)", len(e.Violations))

	for i, v := range e.Violations {
		sep := "; "
		if i == 0 {
			sep = ": "
		}

		fmt.Fprintf(&sb, "%s%s", sep, v.Path)

		if v.Behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			fmt.Fprintf(&sb, " (%s)", v.Behavior.String())
		}

		if v.Reason != "" {
			fmt.Fprintf(&sb, " %s", v.Reason)
		}
	}

	return sb.String()
}

// ErrBehaviorViolation classifies a [BehaviorViolationError].
var ErrBehaviorViolation = errors.New("fieldbehavior: field behavior violation")

// Unwrap classifies the failure; Violations carries each path and annotation.
func (e *BehaviorViolationError) Unwrap() error { return ErrBehaviorViolation }

// appendFormatViolations appends the format violations vs as behavior
// violations: Behavior FIELD_BEHAVIOR_UNSPECIFIED, Format the format name,
// Reason "value <reason> (<format>)".
func appendFormatViolations(dst []BehaviorViolation, vs []fieldinfo.Violation) []BehaviorViolation {
	for _, v := range vs {
		dst = append(dst, BehaviorViolation{Path: v.Path, Reason: "value " + v.Reason + " (" + v.Format + ")", Format: v.Format})
	}

	return dst
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import "errors"

// Client errors: a resource name sent by a client is invalid. Each cause
// below matches [ErrInvalidName]. The checks of a [Validator] report them in
// a [coreerrs.FieldError] on the request field, whose Err is a *[NameError]
// holding the cause.
var (
	// ErrInvalidName matches every invalid resource name.
	ErrInvalidName = errors.New("resourcename: invalid resource name")
	// ErrNoMatch reports a name that matches none of the expected patterns.
	ErrNoMatch error = &nameCause{msg: "resourcename: name does not match the pattern"}
	// ErrInvalidID reports a resource ID segment the ID policy refuses (see
	// [WithIDValidator]).
	ErrInvalidID error = &nameCause{msg: "resourcename: invalid resource ID"}
	// ErrWildcard reports a "-" wildcard segment where the read does not
	// allow one (see [WithWildcard]).
	ErrWildcard error = &nameCause{msg: "resourcename: wildcard not allowed"}
	// ErrInvalidFullName reports a malformed full resource name, a service
	// that differs from the resource type's, or a full name where only
	// relative names are accepted (see [WithFullNames]).
	ErrInvalidFullName error = &nameCause{msg: "resourcename: invalid full resource name"}
	// ErrLimitExceeded reports a name longer than [WithMaxNameLength] or
	// with more segments than [WithMaxSegments] allow.
	ErrLimitExceeded error = &nameCause{msg: "resourcename: name limit exceeded"}
	// ErrInvalidUID reports a uid that is not a UUID4 (AIP-148).
	ErrInvalidUID = errors.New("resourcename: uid is not a UUID4")
)

// Configuration errors: a pattern, a resource type or an annotation the
// server declares is invalid. They are programming errors, never a client's
// fault, and are not field errors.
var (
	// ErrInvalidPattern reports a malformed resource name pattern.
	ErrInvalidPattern = errors.New("resourcename: invalid pattern")
	// ErrInvalidResource reports an invalid resource type or descriptor.
	ErrInvalidResource = errors.New("resourcename: invalid resource")
	// ErrNoResource reports a message without a google.api.resource
	// annotation.
	ErrNoResource = errors.New("resourcename: message has no google.api.resource annotation")
	// ErrUnknownType reports a resource reference to a type the
	// [Validator] has not registered.
	ErrUnknownType = errors.New("resourcename: unknown resource type")
	// ErrDuplicateType reports a resource type registered twice with
	// different definitions.
	ErrDuplicateType = errors.New("resourcename: duplicate resource type")
	// ErrInvalidValue reports a pattern variable without a value, or with an
	// empty one or one holding "/", passed to [Pattern.Format].
	ErrInvalidValue = errors.New("resourcename: invalid pattern variable value")
)

// nameCause is a client error cause: it matches [ErrInvalidName].
type nameCause struct{ msg string }

func (e *nameCause) Error() string { return e.msg }

func (e *nameCause) Unwrap() error { return ErrInvalidName }

// NameError describes why a resource name is invalid. It matches
// [ErrInvalidName] and its Cause (one of the client error sentinels).
type NameError struct {
	// Segment is the zero-based index of the offending segment of the
	// relative name, or -1 when the name as a whole is wrong.
	Segment int
	// Reason says what is wrong, in client-safe words without the name.
	Reason string
	// Cause is the client error sentinel, such as [ErrNoMatch].
	Cause error
}

// Error returns "resourcename: <reason>".
func (e *NameError) Error() string { return "resourcename: " + e.Reason }

// Unwrap returns [ErrInvalidName] and the cause.
func (e *NameError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrInvalidName}
	}

	return []error{ErrInvalidName, e.Cause}
}

func nameError(cause error, segment int, reason string) *NameError {
	return &NameError{Segment: segment, Reason: reason, Cause: cause}
}

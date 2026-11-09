// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable

import (
	"context"
	"errors"
	"fmt"
	"strings"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ReturnPartialSuccessField is the API name of the AIP-217 request field, the
// field [CheckGranularity] reports.
const ReturnPartialSuccessField = "returnPartialSuccess"

// Sentinel errors classify failures independently of diagnostic text.
var (
	// ErrUnreachable matches every *[Error]: resources were unreachable and
	// the request could not succeed partially. It maps to UNAVAILABLE.
	ErrUnreachable = errors.New("unreachable: resources are unreachable")
	// ErrInvalidName reports a name that is not a service-relative resource
	// name (AIP-122): empty, a full name, a URI or a bare ID.
	ErrInvalidName = errors.New("unreachable: invalid resource name")
	// ErrInvalidSource reports a [Source] without a fetch function or with a
	// name another source already has.
	ErrInvalidSource = errors.New("unreachable: invalid source")
	// ErrPartialSuccessUnsupported reports return_partial_success on a request
	// scoped beyond the supported granularity; [CheckGranularity] returns it
	// inside a [coreerrs.FieldError] (INVALID_ARGUMENT).
	ErrPartialSuccessUnsupported = errors.New("unreachable: partial success is not supported for this request scope")
)

// Error reports unreachable resources of a request that cannot succeed
// partially: return_partial_success is false, a required source is
// unreachable, or a fetch reports finer-scoped names with [NewError]. It
// matches [ErrUnreachable] and [coreerrs.ErrUnavailable] (UNAVAILABLE) and,
// when set, Err.
type Error struct {
	// Names are the sorted service-relative names of the unreachable
	// resources, bounded like the response field.
	Names []string
	// Truncated is true when Names omits names over the limit.
	Truncated bool
	// Err is the fetch error that made a resource unreachable, if any.
	Err error
}

// NewError returns an *[Error] for names. A fetch returns it to report the
// most appropriately scoped unreachable resources (a zone of the region the
// source covers, say) instead of the source name; [Run] keeps the items the
// fetch returned along with it.
func NewError(names ...string) *Error {
	return &Error{Names: names}
}

// Error lists the names.
func (e *Error) Error() string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "unreachable: %d resource(s) unreachable", len(e.Names))

	if len(e.Names) > 0 {
		sb.WriteString(": ")
		sb.WriteString(strings.Join(e.Names, ", "))
	}

	if e.Truncated {
		sb.WriteString(", ...")
	}

	return sb.String()
}

// Unwrap returns [ErrUnreachable], [coreerrs.ErrUnavailable] and, when set,
// Err.
func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrUnreachable, coreerrs.ErrUnavailable}
	}

	return []error{ErrUnreachable, coreerrs.ErrUnavailable, e.Err}
}

// SourceError reports a fetch that failed with an error that does not make
// the source unreachable (see [WithClassifier]); it fails [Run]. It matches
// the fetch error.
type SourceError struct {
	// Source is the name of the source.
	Source string
	// Err is the fetch error.
	Err error
}

// Error names the source and the cause.
func (e *SourceError) Error() string {
	return "unreachable: source " + e.Source + ": " + e.Err.Error()
}

// Unwrap returns the fetch error.
func (e *SourceError) Unwrap() error { return e.Err }

// IsUnreachable is the default classifier of [Run]: an error matching
// [ErrUnreachable], [coreerrs.ErrUnavailable] (an *[Error] included) or
// [context.DeadlineExceeded] makes a source unreachable. [Run] checks the
// caller's context first, so its own deadline fails the call instead.
func IsUnreachable(err error) bool {
	return errors.Is(err, ErrUnreachable) || errors.Is(err, coreerrs.ErrUnavailable) ||
		errors.Is(err, context.DeadlineExceeded)
}

// CheckGranularity returns a [coreerrs.FieldError] on
// [ReturnPartialSuccessField] (INVALID_ARGUMENT) wrapping
// [ErrPartialSuccessUnsupported] when a request sets return_partial_success
// but its scope is beyond what the API can report unreachable resources for
// (AIP-217 "Partial success granularity"), and nil otherwise. supported is
// the API's own decision, typically whether the request reads across
// collections (a "-" parent).
func CheckGranularity(returnPartialSuccess, supported bool) error {
	if !returnPartialSuccess || supported {
		return nil
	}

	return &coreerrs.FieldError{
		Field:  ReturnPartialSuccessField,
		Reason: "partial success is not supported for this request scope",
		Err:    ErrPartialSuccessUnsupported,
	}
}

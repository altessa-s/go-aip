// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import (
	"errors"
	"fmt"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// Sentinel errors. Each is classified by a canonical go-atlas-core sentinel
// too, so a transport maps it to a status code without knowing this package
// (errors.Is matches both).
var (
	// ErrInvalidRequestID reports a request ID that does not match the
	// [Format] of the guard. [Do] returns it inside a
	// *[coreerrs.FieldError] on the request ID field (INVALID_ARGUMENT).
	ErrInvalidRequestID error = &classified{msg: "requestid: invalid request ID", class: coreerrs.ErrInvalidArgument}
	// ErrRequestIDReused reports a request ID already used, in the same
	// scope, by a request with other parameters. It matches
	// [coreerrs.ErrAlreadyExists] (ALREADY_EXISTS): the ID names a request
	// that exists, and retrying the same call cannot succeed.
	ErrRequestIDReused error = &classified{msg: "requestid: request ID already used with different request parameters",
		class: coreerrs.ErrAlreadyExists}
	// ErrInProgress reports that the first request with the same ID is
	// still running after the wait of [WithWaitTimeout]. It matches
	// [coreerrs.ErrBusy] (ABORTED): the client retries with the same ID
	// later and then receives the stored response.
	ErrInProgress error = &classified{msg: "requestid: a request with the same request ID is in progress",
		class: coreerrs.ErrBusy}
	// ErrResponseNotRetained reports a duplicate of a request that
	// succeeded but whose response was larger than [WithMaxResponseSize]
	// and was not stored. It matches [coreerrs.ErrFailedPrecondition]; the
	// service may catch it and return the current state of the resource
	// instead (the AIP-155 stale success response).
	ErrResponseNotRetained error = &classified{msg: "requestid: the response of the original request was not retained",
		class: coreerrs.ErrFailedPrecondition}
	// ErrStore reports a failure of the [Store]. It matches
	// [coreerrs.ErrUnavailable] (UNAVAILABLE) and wraps the store's error.
	// The handler never runs when the reservation fails.
	ErrStore error = &classified{msg: "requestid: store failure", class: coreerrs.ErrUnavailable}
	// ErrStoreFull reports that a [MemoryStore] holds its maximum number of
	// live entries; [Do] returns it wrapped in [ErrStore].
	ErrStoreFull = errors.New("requestid: store is full")
	// ErrReservationLost reports a [Store.Complete] or [Store.Release] by a
	// caller that no longer owns the reservation: it expired, was taken
	// over, completed or released.
	ErrReservationLost = errors.New("requestid: reservation lost")
	// ErrNoRequestIDField reports a request message without a singular
	// string field of the name set by [WithField]: a programming error.
	ErrNoRequestIDField = errors.New("requestid: request has no request ID field")
	// ErrInvalidScope reports a [Scope] without a method.
	ErrInvalidScope = errors.New("requestid: scope has no method")
	// ErrReplayedFailure matches every *[ReplayedError].
	ErrReplayedFailure = errors.New("requestid: replayed failure")
)

// classified is a sentinel that also matches a canonical class.
type classified struct {
	msg   string
	class error
}

func (e *classified) Error() string { return e.msg }

func (e *classified) Unwrap() error { return e.class }

// invalidRequestIDError is an [ErrInvalidRequestID] with its reason.
type invalidRequestIDError struct{ reason string }

func (e *invalidRequestIDError) Error() string { return e.reason }

func (e *invalidRequestIDError) Unwrap() error { return ErrInvalidRequestID }

func invalidf(format string, args ...any) error {
	return &invalidRequestIDError{reason: fmt.Sprintf(format, args...)}
}

// ReplayedError is the failure of a duplicate request whose original failed
// with an error that [WithCacheError] chose to store. Status is the stored
// google.rpc.Status; a transport returns it as is, so the duplicate fails
// exactly as the original did. It matches [ErrReplayedFailure].
type ReplayedError struct {
	// Status is the stored status of the original failure.
	Status *statuspb.Status
}

// Error renders the code and message of the stored status.
func (e *ReplayedError) Error() string {
	return fmt.Sprintf("requestid: replayed failure: code %d: %s", e.Status.GetCode(), e.Status.GetMessage())
}

// Unwrap returns [ErrReplayedFailure].
func (e *ReplayedError) Unwrap() error { return ErrReplayedFailure }

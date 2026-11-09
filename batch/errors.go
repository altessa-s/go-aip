// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Sentinel errors classify batch failures independently of diagnostic text.
// The request checks return them inside a [coreerrs.FieldError] (so they
// also match [coreerrs.ErrInvalidArgument]); an item check inside an
// *[ItemError] as well.
var (
	// ErrEmpty reports a batch without items.
	ErrEmpty = errors.New("batch: no items")
	// ErrTooManyItems reports a batch with more items than [WithMaxItems]
	// allows.
	ErrTooManyItems = errors.New("batch: too many items")
	// ErrParentMismatch reports an item whose parent does not match the
	// parent of the batch request.
	ErrParentMismatch = errors.New("batch: parent mismatch")
	// ErrHoistedMismatch reports an item whose value of a hoisted field
	// differs from the value of the batch request.
	ErrHoistedMismatch = errors.New("batch: hoisted field mismatch")
	// ErrDuplicate reports an item naming a resource an earlier item names.
	ErrDuplicate = errors.New("batch: duplicate item")
	// ErrNotFound reports a resource of a Batch Get that does not exist. It
	// matches [coreerrs.ErrNotFound], so the batch fails with NOT_FOUND.
	ErrNotFound = fmt.Errorf("batch: resource %w", coreerrs.ErrNotFound)
	// ErrAllFailed reports a partial-success batch in which no request
	// succeeded. AIP-233 to AIP-235 require the operation error to be
	// ABORTED then, with the message of [AllFailedMessage].
	ErrAllFailed = errors.New("batch: none of the requests succeeded")
	// ErrNoTransaction reports an [Atomic] call without a transaction
	// runner, or a runner that returned success without running the batch.
	ErrNoTransaction = errors.New("batch: atomic execution requires a transaction runner")
	// ErrNotBatchRequest reports a message [ValidateRequest] cannot read: it
	// has neither a repeated message "requests" field nor a repeated string
	// "names" field.
	ErrNotBatchRequest = errors.New("batch: not a batch request message")
)

// ItemError is the failure of the item at Index of the repeated request
// field Field ("requests" or "names"), whose request path is
// Field + "[" + Index + "]" ([ItemError.Path]).
//
// It matches every error Err matches. When Err holds a
// [coreerrs.FieldError], the ItemError also yields one re-rooted under the
// item path, so a field violation of a nested request ("book.title") is
// reported as "requests[2].book.title"; an Err that matches
// [coreerrs.ErrInvalidArgument] without a field yields a FieldError on the
// item path itself. Any other Err (a NOT_FOUND, a conflict) keeps its own
// classification. A FieldError or sentinel a classified error hides
// (apierror.Classify) is not in the chain of Err and never yields one.
type ItemError struct {
	// Index is the position of the item in the repeated field.
	Index int
	// Field is the JSON name of the repeated field.
	Field string
	// Err is the failure of the item, as the singular method returns it.
	Err error
}

// NewItemError returns err as the *[ItemError] of the item at index of
// field, or nil when err is nil.
func NewItemError(field string, index int, err error) error {
	if err == nil {
		return nil
	}

	return &ItemError{Index: index, Field: field, Err: err}
}

// Path returns the request path of the item, such as "requests[2]".
func (e *ItemError) Path() string {
	return e.Field + "[" + strconv.Itoa(e.Index) + "]"
}

// Error renders "batch: <path>: <item error>" (without the "batch: " prefix
// of a sentinel of this package), or "batch: <path>.<field>:
// <reason>" when Err is a [coreerrs.FieldError].
func (e *ItemError) Error() string {
	switch err := e.Err.(type) {
	case nil:
		return "batch: " + e.Path() + ": failed"
	case *coreerrs.FieldError:
		return "batch: " + joinPath(e.Path(), err.Field) + ": " + err.Reason
	default:
		return "batch: " + e.Path() + ": " + strings.TrimPrefix(err.Error(), "batch: ")
	}
}

// Unwrap returns the field error of the item path described on
// [ItemError], if any, followed by Err.
func (e *ItemError) Unwrap() []error {
	if e.Err == nil {
		return nil
	}

	if fe, ok := errors.AsType[*coreerrs.FieldError](e.Err); ok {
		return []error{&coreerrs.FieldError{Field: joinPath(e.Path(), fe.Field), Reason: fe.Reason, Err: fe.Err}, e.Err}
	}

	if errors.Is(e.Err, coreerrs.ErrInvalidArgument) {
		return []error{&coreerrs.FieldError{Field: e.Path(), Reason: e.Err.Error(), Err: e.Err}}
	}

	return []error{e.Err}
}

// AllFailedMessage returns the message AIP-233 to AIP-235 require for the
// ABORTED operation error of a partial-success batch in which no request
// succeeded ([ErrAllFailed]); metadataType is the operation metadata message,
// such as "BatchCreateBooksOperationMetadata".
func AllFailedMessage(metadataType string) string {
	return "None of the requests succeeded, refer to the " + metadataType +
		".failed_requests for individual error details"
}

// fieldError is the field error of the item-relative path with cause.
func fieldError(path, reason string, cause error) error {
	return &coreerrs.FieldError{Field: path, Reason: reason, Err: cause}
}

// joinPath appends the item-relative path rel to the item path.
func joinPath(item, rel string) string {
	if rel == "" {
		return item
	}

	return item + "." + rel
}

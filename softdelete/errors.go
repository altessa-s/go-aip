// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete

import (
	"errors"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Request field names of AIP-165 field errors.
const (
	FieldParent = "parent"
	FieldFilter = "filter"
)

// Sentinel errors. Each matches the go-atlas-core canonical error its gRPC
// code maps from, so a transport classifies it without knowing this package.
var (
	// ErrNotFound reports a resource that does not exist: it was never
	// created, or its purge time has passed. It matches [coreerrs.ErrNotFound].
	ErrNotFound error = &kindError{msg: "softdelete: resource not found", kinds: []error{coreerrs.ErrNotFound}}
	// ErrAlreadyDeleted reports Delete of a soft-deleted resource without
	// allow_missing (AIP-164: NOT_FOUND). It matches [ErrNotFound] and
	// [coreerrs.ErrNotFound].
	ErrAlreadyDeleted error = &kindError{msg: "softdelete: resource is already deleted",
		kinds: []error{ErrNotFound, coreerrs.ErrNotFound}}
	// ErrNotDeleted reports Undelete of a resource that is not soft-deleted
	// (AIP-164: ALREADY_EXISTS). It matches [coreerrs.ErrAlreadyExists].
	ErrNotDeleted error = &kindError{msg: "softdelete: resource is not deleted", kinds: []error{coreerrs.ErrAlreadyExists}}
	// ErrTooManyResources reports a forced purge that matches more resources
	// than [WithMaxPurgeCount] allows; it is the cause of a field error on
	// filter.
	ErrTooManyResources = errors.New("softdelete: purge matches too many resources")
	// ErrFilterTooLong reports a purge filter longer than
	// [WithMaxFilterLength] allows; it is the cause of a field error on
	// filter.
	ErrFilterTooLong = errors.New("softdelete: filter exceeds maximum length")
	// ErrUnsupportedResource reports a resource whose descriptor lacks a
	// valid delete time field or holds an invalid purge time field. It is a
	// server configuration error, not a client error.
	ErrUnsupportedResource = errors.New("softdelete: unsupported resource")
	// ErrInvalidPurger reports a [Purger] built without a policy, parser or
	// store.
	ErrInvalidPurger = errors.New("softdelete: invalid purger")
	// ErrInvalidStoreResult reports a [Store] that returned a negative
	// count.
	ErrInvalidStoreResult = errors.New("softdelete: invalid store result")
)

// kindError is a sentinel that also matches the canonical errors in kinds.
type kindError struct {
	msg   string
	kinds []error
}

func (e *kindError) Error() string { return e.msg }

func (e *kindError) Unwrap() []error { return e.kinds }

// fieldError is an InvalidArgument field error on field.
func fieldError(field, reason string, cause error) error {
	return &coreerrs.FieldError{Field: field, Reason: reason, Err: cause}
}

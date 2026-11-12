// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

import (
	"errors"

	"github.com/altessa-s/go-aip/internal/maskpath"

	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
)

var (
	// ErrInvalidPath reports a path that does not address a valid field.
	// Every *[ValidationError] and every limit error matches it, as do the
	// path errors of the update package.
	ErrInvalidPath = maskpath.ErrInvalidPath

	// ErrWildcardNotAlone is returned by [FieldMask.SelectsAll] (and the
	// methods built on it) for a mask that combines the full-replacement path
	// "*" with other paths: it neither replaces the whole message nor names
	// fields.
	ErrWildcardNotAlone = errors.New(`fieldmask: "*" must be the only path`)
)

// Default limits of a field mask, which bound the work a client mask can
// cause (see [WithMaxPathDepth], [WithMaxPaths] and [WithMaxPathLength]).
const (
	// DefaultMaxPathDepth is the default maximum number of segments in a
	// path, "*" and quoted keys included.
	DefaultMaxPathDepth = 32
	// DefaultMaxPaths is the default maximum number of paths in a mask.
	DefaultMaxPaths = 1000
	// DefaultMaxPathLength is the default maximum length of a raw path in
	// bytes.
	DefaultMaxPathLength = 1024
)

// Limit errors. Each matches [ErrLimitExceeded] and [ErrInvalidPath]; the
// entry points wrap them in their structured errors (a *[ValidationError],
// or the field error of the update package's planning).
var (
	// ErrLimitExceeded matches every limit error below.
	ErrLimitExceeded = maskpath.ErrLimitExceeded
	// ErrMaxPathDepthExceeded reports a path with more segments than
	// [WithMaxPathDepth] allows.
	ErrMaxPathDepthExceeded = maskpath.ErrMaxPathDepthExceeded
	// ErrMaxPathsExceeded reports a mask with more paths than [WithMaxPaths]
	// allows.
	ErrMaxPathsExceeded = maskpath.ErrMaxPathsExceeded
	// ErrMaxPathLengthExceeded reports a path longer than
	// [WithMaxPathLength] allows.
	ErrMaxPathLengthExceeded = maskpath.ErrMaxPathLengthExceeded
)

// Unwrap classifies a path failure as [ErrInvalidPath] and, when set, by its
// Cause.
func (e *ValidationError) Unwrap() []error {
	return coreslices.AppendNonNil([]error{ErrInvalidPath}, e.Cause)
}

// validationError returns err, an error of the path grammar, as the
// *ValidationError of the path it names; any other error as it is.
func validationError(err error) error {
	if e, ok := errors.AsType[*maskpath.Error](err); ok {
		return &ValidationError{Path: e.Path, Reason: e.Reason}
	}

	return err
}

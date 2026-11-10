// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

// ValidationError is returned by [FieldMask.Validate] and every other path
// check when a field path is malformed or does not match the protobuf message
// schema. Path is the offending path as written and Reason explains what went
// wrong (missing field, index access, malformed segment, etc.). Cause, when
// set, is a more specific sentinel such as [ErrWildcardNotAlone]; the error
// always matches [ErrInvalidPath].
type ValidationError struct {
	Path   string // The invalid path.
	Reason string // Why the path is invalid.
	Cause  error  // An optional more specific classification.
}

// Error returns the error message. A mask-level error, such as the path
// count limit, has no Path and names the mask instead.
func (e *ValidationError) Error() string {
	if e.Path == "" {
		return "invalid field mask: " + e.Reason
	}

	return "invalid field mask path '" + e.Path + "': " + e.Reason
}

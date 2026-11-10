// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

// PathValidationMode controls how [Apply] treats field-mask
// paths that the path grammar or the message schema rejects (AIP-161). Set it
// via [WithPathValidation].
type PathValidationMode int

const (
	// PathValidationEnforce rejects the update with a *[fieldmask.ValidationError] before any
	// mutation, so a typo cannot clear a sibling subtree as a side effect (fail-fast).
	// This is the default: AIP-161 requires an invalid path in an update mask to be
	// an INVALID_ARGUMENT error.
	PathValidationEnforce PathValidationMode = iota

	// PathValidationWarn reports every invalid path to the reporter set by
	// [WithPathValidationReporter] but still applies the mask, as
	// PathValidationDisabled does. Use it as an explicit opt-out to surface bad paths
	// (e.g. client typos) without breaking callers while a fix rolls out. With no
	// reporter it is a silent no-op.
	PathValidationWarn

	// PathValidationDisabled is an explicit opt-out that ignores invalid paths: such
	// a path simply matches no field and is skipped, so a mistyped path clears the
	// field it was meant to keep. An index into a repeated field or a ListValue,
	// a path into a message kept whole by [WithAtomicMessage] (whatever follows
	// it, "delete_time.typo" included), and with WithStrictDynamicPaths a path
	// below a google.protobuf.Struct key or into a Value or ListValue, are still
	// rejected.
	PathValidationDisabled
)

// shouldValidatePaths reports whether Apply must run the schema-path
// pass. Warn without a reporter has nowhere to surface results, so it is a
// no-op and skips the walk entirely.
func (o *options) shouldValidatePaths() bool {
	switch o.pathValidation {
	case PathValidationEnforce:
		return true
	case PathValidationWarn:
		return o.pathReporter != nil
	default:
		return false
	}
}

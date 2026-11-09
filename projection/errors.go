// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import "errors"

// Sentinel errors for the projection package. Callers classify with
// [errors.Is]; the message attached at the wrap site carries the offending
// path or limit. Request-time errors — those of the parser, [Policy.Validate],
// [Policy.Select] and the translators' Translate methods — map to
// INVALID_ARGUMENT; construction errors are configuration faults.
var (
	// ErrDuplicatePath indicates a repeated path when rejection is enabled.
	ErrDuplicatePath = errors.New("duplicate projection path")
	// ErrUnsupportedSelection indicates a selection mode the backend cannot render.
	ErrUnsupportedSelection = errors.New("unsupported projection selection")
	// ErrEmptyClause indicates a path between commas was empty or
	// whitespace-only (e.g. "a,,b" or a leading/trailing comma).
	ErrEmptyClause = errors.New("empty fields clause")

	// ErrInvalidFieldPath indicates a path that does not match the grammar:
	// a non-identifier character, a leading digit, or an empty segment from
	// consecutive dots. Translators also return it for a storage name the
	// backend cannot address (a SQL column must be `col` or `table.col`).
	ErrInvalidFieldPath = errors.New("invalid field path")

	// ErrUnsupportedPath indicates field mask syntax this package does not
	// serve: wildcard segments ("items.*.name", "a,*") and backtick-quoted map
	// keys, which AIP-161 makes optional, and numeric segments ("items.0"),
	// which AIP-161 forbids — a read mask must not index a repeated field, and
	// the request must fail with INVALID_ARGUMENT, which is how this error
	// maps.
	ErrUnsupportedPath = errors.New("unsupported field path syntax")

	// ErrExpressionTooLong indicates the raw input exceeds the configured
	// MaxExpressionLength.
	ErrExpressionTooLong = errors.New("fields expression length exceeds maximum allowed")

	// ErrMaxPathsExceeded indicates the number of paths exceeds the
	// configured MaxPaths. The limit applies before deduplication.
	ErrMaxPathsExceeded = errors.New("number of field paths exceeds maximum allowed")

	// ErrMaxFieldPathDepthExceeded indicates a path has more dot-separated
	// segments than MaxFieldPathDepth.
	ErrMaxFieldPathDepthExceeded = errors.New("field path depth exceeds maximum allowed")

	// ErrMaxFieldNameLengthExceeded indicates a path is longer than
	// MaxFieldNameLength.
	ErrMaxFieldNameLengthExceeded = errors.New("field name length exceeds maximum allowed")

	// ErrFieldNotAllowed indicates a requested path is absent from the
	// allow-list, or overlaps a denied field — equal to it, an ancestor of
	// it, or a descendant of it.
	ErrFieldNotAllowed = errors.New("field not allowed")

	// ErrAllowlistRequired indicates [NewPolicy] was given WithUntrustedInput
	// without an allow-list that admits some but not every field: a
	// restricting WithAllowedFields list or a restricting
	// fieldpolicy.AllowList given to WithFieldAccess. An opaque
	// WithFieldAccess policy needs a WithAllowedFields list beside it.
	ErrAllowlistRequired = errors.New("allowlist is required for untrusted input")

	// ErrPolicyRequired indicates a translator was constructed without a
	// [Policy]: every translator renders only projections its policy validated.
	ErrPolicyRequired = errors.New("projection policy is required")

	// ErrConflictingPolicy indicates policy or translator options that
	// contradict each other: a required field that is denied, or a view field
	// outside the allow-list, overlapping a denied field or reaching a denied
	// storage path. A denied or view path outside the path grammar fails
	// [NewPolicy] with [ErrInvalidFieldPath] or [ErrUnsupportedPath] instead.
	ErrConflictingPolicy = errors.New("conflicting projection policy")

	// ErrAllFieldsUnresolvable indicates every authorized field — what an
	// omitted read mask, "*" and ViewFull return (AIP-157) — cannot be
	// enumerated safely: an empty allow-list or one with embedded wildcards,
	// an allow-list root that overlaps a denied field or reaches a denied
	// storage path, a field access policy without an allow-list, or a SQL
	// backend with denied fields and no allow-list. List the allowed fields
	// so that their roots carry nothing denied.
	ErrAllFieldsUnresolvable = errors.New("all authorized fields cannot be resolved")

	// ErrInvalidView indicates a view the policy does not serve: at
	// construction a WithView of ViewUnspecified, ViewFull, a negative view
	// or an empty path list, or a WithDefaultView that is neither ViewFull
	// nor configured; at request time a view that is not ViewUnspecified,
	// ViewFull or configured (INVALID_ARGUMENT).
	ErrInvalidView = errors.New("invalid resource view")
)

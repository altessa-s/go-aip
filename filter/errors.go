// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"errors"

	"github.com/altessa-s/go-aip/filter/regexanchor"
)

// Sentinel errors for the filter package.
var (
	// ErrParseFailed indicates that CEL expression parsing failed.
	ErrParseFailed = errors.New("failed to parse CEL expression")

	// ErrUnsupportedOperation indicates an operation that cannot be translated.
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrFieldNotAllowed indicates that a field is not in the allowed list.
	ErrFieldNotAllowed = errors.New("field not allowed")

	// ErrFunctionNotAllowed indicates that a CEL function (built-in or
	// custom) is not in the allowed list configured via
	// [github.com/altessa-s/go-aip/filter/parser.WithAllowedFunctions].
	// Operators (==, !=, <, &&, ||, !, in) and the has() macro are part of the baseline grammar and are not
	// subject to this whitelist. Translators and evaluators also return it
	// for matches() under [WithUntrustedInput] without [WithSafeRegex].
	ErrFunctionNotAllowed = errors.New("function not allowed")

	// ErrAllowlistRequired indicates a policy built by [NewPolicy] without a
	// field allowlist: no non-empty [WithAllowedFields] and no restricting
	// [WithFieldAccess] (a configured, non-empty fieldpolicy.AllowList that is
	// not "*", or an opaque fieldpolicy.Access on trusted input), while
	// neither opting out with [WithAnyField] nor allowed to because of
	// [WithUntrustedInput]. Translating client CEL without an allowlist would
	// let the caller filter on any field — including ones the application
	// never intended to expose — so the policy is refused.
	ErrAllowlistRequired = errors.New("filter field allowlist is required")

	// ErrMaxDepthExceeded indicates the expression nesting exceeds the configured limit.
	ErrMaxDepthExceeded = errors.New("expression depth exceeds maximum allowed")

	// ErrInvalidExpression indicates a structurally invalid expression.
	ErrInvalidExpression = errors.New("invalid expression structure")

	// ErrUnsupportedType indicates an unsupported literal or value type.
	ErrUnsupportedType = errors.New("unsupported type")

	// ErrFieldTypeMismatch indicates a literal in the filter is not assignable
	// to the kind declared for the field via [WithFieldTypes].
	ErrFieldTypeMismatch = errors.New("filter value type does not match field type")

	// ErrEnumValueNotAllowed indicates an integer literal in the filter is not in
	// the value set declared for the field via [WithEnumValues].
	ErrEnumValueNotAllowed = errors.New("filter value is not an allowed enum value")

	// ErrEmptyExpression indicates an empty or whitespace-only expression was provided.
	ErrEmptyExpression = errors.New("expression cannot be empty")

	// ErrInvalidRegex indicates a regex pattern that is invalid or too complex.
	ErrInvalidRegex = regexanchor.ErrInvalidRegex

	// ErrMaxOperationsExceeded indicates the expression requires more operations
	// than the configured limit allows.
	ErrMaxOperationsExceeded = errors.New("operation count exceeds maximum allowed")

	// ErrDataLimitExceeded indicates that evaluating the expression against the data would spend more work than
	// [github.com/altessa-s/go-aip/filter/evaluator.WithMaxDataBudget] allows: the data is too large, or shares its parts
	// so that a path through it multiplies (a map holding a list of itself), for the filter. Both evaluator modes return it,
	// whatever the expression would otherwise evaluate to.
	ErrDataLimitExceeded = errors.New("data evaluation budget exceeded")

	// ErrExpressionTooLong indicates the expression exceeds the configured maximum length.
	ErrExpressionTooLong = errors.New("expression length exceeds maximum allowed")

	// ErrPolicyRequired indicates a translator or evaluator built without a filter [Policy].
	ErrPolicyRequired = errors.New("filter policy is required")

	// ErrInvalidOptions indicates options that contradict each other or are malformed, such as a
	// [github.com/altessa-s/go-aip/filter/parser.WithEnumNames] vocabulary with a value that is not a string or an integer.
	// Constructors return it, so the misconfiguration surfaces at process start.
	ErrInvalidOptions = errors.New("invalid filter options")
)

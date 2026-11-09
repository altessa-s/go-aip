// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package filter defines the shared filter AST, field vocabulary, immutable query policy and backend-neutral validator.
//
// Construct an immutable Policy with NewPolicy and validate a shared Node AST before evaluation or translation. A nonempty field allowlist,
// or a WithFieldAccess policy that restricts the fields, is required unless WithAnyField(true) explicitly permits trusted fields; NewPolicy
// fails with [ErrAllowlistRequired] otherwise. WithUntrustedInput(true) always requires an allowlist and disallows unsafe regex
// configuration. Every translator and evaluator constructor fails with [ErrPolicyRequired] for a nil policy, and with [ErrInvalidOptions]
// for contradictory options; evaluation beyond the evaluator's data budget fails with [ErrDataLimitExceeded].
//
// Backend translators implement the neutral ports declared here: Translator[Out] for rendering one query value (a SQL
// translator returns its bind arguments as well), Compatibility for configured validation without I/O, and Telemetry for
// translation duration.
//
// # Usage
//
//	policy, err := filter.NewPolicy(filter.WithAllowedFields("name"))
//	err = filter.Validate(node, policy)
//	nnf := filter.NegationNormalForm(node, nil, map[string]bool{"rating": true})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package filter

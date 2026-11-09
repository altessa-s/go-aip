// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package evaluator evaluates filter ASTs over Go values with bounded work, replaceable comparison, and explicit semantic profiles.
//
// New takes the shared *filter.Policy and evaluator Option values; WithFilterOptions maps the policy's API names to data keys.
// Evaluate validates the expression and evaluates one value, while Prepare snapshots and validates once for repeated execution.
// A nil filter matches every document. A null or absent operand follows SQL's three-valued logic, as the SQL translators render
// it: an unknown predicate is absorbed by && and ||, stays unknown under !, and an unknown filter does not match.
//
// WithProfile selects an explicit semantic contract. A Profile validates the policy; a Compiler returns a Program, a Binder returns
// the Compiler bound to the evaluator's ValueEncoding, and a ValueDecoder adapts driver-owned values. These contracts name no backend
// and carry no database-driver dependency; concrete profiles live with their backends (postgres.Schema, mongo.Schema). A profile's
// Program reads data with the evaluator's building blocks: a Reader (stored forms, paths, the data budget), a Guard (operation and
// depth limits), CompareResult, CompareExact and LiteralPatterns.
//
// # Usage
//
//	policy, err := filter.NewPolicy(filter.WithAllowedFields("age"))
//	eval, err := evaluator.New(policy)
//	matched, err := eval.Evaluate(node, data)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package evaluator

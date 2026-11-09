// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package evaluation implements portable proto3 listing semantics as a driver-independent filter evaluator profile.
//
// New(schema, reader) builds a filter/evaluator profile for the listing contract without a database driver. FieldReader resolves stored
// values; NewReader(encoding, maxWork) supplies bounded dotted traversal with optional storage conversion.
//
// # Usage
//
//	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 1000))
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package evaluation

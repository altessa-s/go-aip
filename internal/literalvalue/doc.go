// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package literalvalue defines immutable cached literal values and their type-tagged canonical encoding.
//
// This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.
// Mutable work state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.
//
// # Usage
//
//	fingerprint := literalvalue.Canonical(int64(42))
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package literalvalue

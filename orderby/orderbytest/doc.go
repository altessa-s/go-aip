// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package orderbytest provides order_by parsing and policy helpers for tests of orderby consumers (translators, listing engines), and the
// conformance suite every orderby translator runs ([RunConformance]): shared policy and storage-option rows with one outcome for
// every backend, and capability rows (multiple keys, null placement, collation) whose outcome each backend declares.
//
// Test helpers parse expressions, build policies and report failures through testing.TB. They use the public parser and policy APIs;
// production callers should construct and configure their own parser and policy.
//
// # Usage
//
//	spec := orderbytest.MustParseOrderBy(t, "created_at desc")
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package orderbytest

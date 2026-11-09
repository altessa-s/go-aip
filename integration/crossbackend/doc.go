// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package crossbackend holds no code: its tests exercise the core packages through several backends at once, so that the
// tests of the core packages and of each backend import no other backend and the root module requires no backend module
// (TestLayering in the repository root enforces both). It is the only package of the test-only module
// integration/crossbackend, which is never released.
//
// # Contents
//
// The test files are named after the package whose behavior they pin:
//
//	aipmap_test.go, exported_errors_test.go
//	                   the code and reason of every backend sentinel, and the check that the table lists them all
//	filter_*_test.go   one policy shared by every filter translator and the evaluator: allow-lists, untrusted input,
//	                   the safe-regex subset, budgets, literal and decimal encoders, concurrency
//	listing_*_test.go  listing profiles, schemas and policies checked by the memory and SQL list engines
//	orderby_*_test.go, projection_*_test.go
//	                   one order or projection policy shared by several translators
//	softdelete_*_test.go
//	                   a soft-delete restriction applied by the memory list engine
//	memory_*_test.go, postgres_*_test.go
//	                   backend tests that compare with or borrow from another backend (MariaDB)
//
// Behavior shared by every backend belongs in the conformance suites (filter/filtertest, orderby/orderbytest,
// projection/projectiontest, listing/listingtest), which each backend runs itself; this package is for checks that need
// several concrete backends in one test.
package crossbackend

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package mariadbtest holds the test fixtures of the MariaDB backend shared by its unit tests and its live-database tests (the
// test-only module integration/docker): [FilterConformanceBackend], the filter translator with the expected outcome of every
// filtertest conformance case, and [DecodeColumns], which reads a rendered column list back for projectiontest.Adapt. It imports no
// database driver, so the module needs no extra dependency for it.
//
// # Usage
//
//	filtertest.RunConformance(t, mariadbtest.FilterConformanceBackend())
//
//	projectiontest.Adapt(tr, mariadbtest.DecodeColumns)
//
// See README.md.
package mariadbtest

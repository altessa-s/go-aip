// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package mongotest holds the test fixtures of the MongoDB backend shared by its unit tests and its live-database tests (the
// test-only module integration/docker): [FilterConformanceBackend], the filter translator with the expected outcome of every
// filtertest conformance case, and the explain helpers ([AssertIndexedSorts], [Explain], [PlanOf]) that prove every order of a
// mongo.ListEngine is an index scan without a blocking sort.
//
// # Usage
//
//	filtertest.RunConformance(t, mongotest.FilterConformanceBackend())
//
//	require.NoError(t, list.EnsureIndexes(ctx))
//	mongotest.AssertIndexedSorts(t, coll, list, bson.D{{Key: "owner", Value: "owner-1"}})
//
// See README.md.
package mongotest

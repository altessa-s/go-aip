// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package testhelpers provides the test fixtures shared across the packages
// of the root module (the backend and test-only modules never import it):
//   - A protobuf update helper ([ApplyLeafPaths]) that copies the fields an
//     update mask names leaf by leaf, as a storage update by leaf paths
//     would, to check which stored values an update leaves intact.
//   - A [metrics.Meter] ([NewRecordingMeter]) that counts the completed
//     timings and increments of every instrument by full metric name.
//   - A source scanner ([ExportedErrors]) that lists the exported Err*
//     variables and error types of a directory tree, for the tests that
//     require every sentinel to have an apierror mapping.
//
// Fixtures tied to a capability live with it: filter/filtertest,
// orderby/orderbytest, pagination/pagetoken/pagetokentest,
// listing/listingtest and backend/sqlbase/sqlbasetest (the scripted
// database/sql driver). Helpers that accept [testing.TB] register
// cleanup functions automatically.
package testhelpers

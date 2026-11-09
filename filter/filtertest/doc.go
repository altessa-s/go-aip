// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package filtertest provides filter-parsing helpers for tests of filter consumers (translators, storages, listing engines), and
// the conformance suite every filter translator and the evaluator run ([RunConformance]): one table of filters covering every
// operator, function and operand shape of the filter language, policy rows with one outcome for every backend, and capability
// rows whose outcome (a golden rendering or a sentinel) each backend declares. A backend that runs its output in-process is also
// compared with the evaluator on shared records ([ConformanceRecords]).
//
// Test helpers parse expressions and report failures through testing.TB. They use the public parser API; production callers should construct
// and configure their own parser.
//
// # Usage
//
//	node := filtertest.MustParseFilter(t, `status == "active"`)
//
//	filtertest.RunConformance(t, filtertest.Backend{New: newTranslate, Expect: expect})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package filtertest

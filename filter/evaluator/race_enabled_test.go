// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build race

package evaluator_test

// raceEnabled reports a race-detector build, whose instrumentation allocates where a normal build does not.
const raceEnabled = true

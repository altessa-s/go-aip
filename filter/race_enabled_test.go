// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build race

package filter_test

// raceSlowdown scales the wall-clock bounds of the tests that bound an evaluation's time: the race detector runs the evaluator
// several times slower, and a loaded machine (the parallel packages of go test ./...) slows it further, while the unbounded shapes
// those tests guard against are slowed by the same factor, so a scaled bound separates them as the unscaled one does without it.
const raceSlowdown = 5

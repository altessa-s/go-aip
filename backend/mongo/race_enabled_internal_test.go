// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build race

package mongo

// raceSlowdown scales the wall-clock bounds of the internal tests that bound a check's time: the race detector runs the
// reflective scope walk several times slower, and the parallel packages of go test -race ./... slow it further
// (TestBoundedScopeFails took up to 0.48 s of its 1 s bound under -race), while the expanded walk those tests guard against is
// slowed by the same factor, so a scaled bound separates them as the unscaled one does without it.
const raceSlowdown = 5

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build race

package keyset

// raceSlowdown scales the wall-clock bounds of the walks over compact shared graphs: the race detector runs the reflective walk
// about fourteen times slower, and the parallel subtests and packages of go test -race ./... contend for the machine, so
// TestValidateFilterRejectsSharedGraphs took up to 1.1 s of its 1 s bound under -race (at most 0.08 s without it). The scaled
// bound leaves more than 2.5x headroom for a loaded machine while an expanded walk, slowed by the same factor, still exceeds it.
const raceSlowdown = 5

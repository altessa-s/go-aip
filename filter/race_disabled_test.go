// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build !race

package filter_test

// raceSlowdown is 1 without the race detector; see race_enabled_test.go.
const raceSlowdown = 1

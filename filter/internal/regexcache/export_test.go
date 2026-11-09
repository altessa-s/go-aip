// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache

// The source scanners [Safe] reads a pattern with, exposed to the backtracking model of the black-box tests, so the model
// splits a pattern into groups and alternations exactly where Safe does.
var (
	SkipEscape = skipEscape
	SkipClass  = skipClass
	RepeatLen  = repeatLen
)

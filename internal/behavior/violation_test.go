// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// TestQuoteSegment pins the AIP-161 quoting of a path segment: a plain
// segment as is, and an empty one or one holding a dot or a backtick between
// backticks with every backtick doubled.
func TestQuoteSegment(t *testing.T) {
	t.Parallel()

	for seg, want := range map[string]string{
		"display_name": "display_name",
		"*":            "*",
		"":             "``",
		"a.b":          "`a.b`",
		"x`y":          "`x``y`",
		"x`y.z":        "`x``y.z`",
		"google.com/x": "`google.com/x`",
		"John Smith":   "John Smith",
	} {
		assert.Equal(t, want, behavior.QuoteSegment(seg), seg)
	}
}

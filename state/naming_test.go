// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/altessa-s/go-aip/state"
)

func TestUpperSnake(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"State":          "STATE",
		"JobState":       "JOB_STATE",
		"HTTPRouteState": "HTTP_ROUTE_STATE",
		"V2State":        "V2_STATE",
		"Lifecycle":      "LIFECYCLE",
	} {
		assert.Equal(t, want, state.UpperSnake(in), in)
	}
}

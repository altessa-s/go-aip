// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComparableWithin_Pin(t *testing.T) {
	t.Parallel()

	type withString struct{ S string }

	type withAny struct{ A any }

	for _, tc := range []struct {
		name       string
		v          any
		budget     int
		canCompare bool
		ok         bool
	}{
		{"no budget", int64(1), 0, false, false},
		{"type inspection out of budget", withString{"x"}, 1, false, false},
		{"fixed-size array out of budget", [64]int64{}, 3, false, false},
		{"fixed-size array", [64]int64{}, 100, true, true},
		{"long string out of budget", string(make([]byte, 1000)), 1, false, false},
		{"interface holding a slice", withAny{[]int{}}, 100, false, true},
		{"interface holding nil", withAny{}, 100, true, true},
		{"array of interfaces", [2]any{1, "s"}, 100, true, true},
		{"array of interfaces out of budget", [2]any{1, "s"}, 3, false, false},
		{"struct fields out of budget", withString{"x"}, 3, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := &dataBudget{left: tc.budget}
			canCompare, ok := comparableWithin(reflect.ValueOf(tc.v), b)
			require.Equal(t, tc.canCompare, canCompare)
			require.Equal(t, tc.ok, ok)
		})
	}
}

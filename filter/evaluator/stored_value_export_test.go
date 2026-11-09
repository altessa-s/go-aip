// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/filter/evaluator"
)

// units is an evaluator.Budget with a fixed number of units.
type units struct{ left int }

func (u *units) Charge(n int) bool {
	if n > u.left {
		u.left = -1
		return false
	}

	u.left -= n

	return true
}

func TestStoredValue(t *testing.T) {
	t.Parallel()

	type label string

	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	seven := 7

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"string", "x", "x"},
		{"named string", label("x"), "x"},
		{"int8", int8(-3), int64(-3)},
		{"uint64 above int64", uint64(1 << 63), uint64(1 << 63)},
		{"uint32", uint32(5), int64(5)},
		{"float32", float32(1.5), 1.5},
		{"time", when, when},
		{"pointer", &seven, 7},
		{"nil pointer", (*int)(nil), nil},
		{"typed slice", []string{"a", "b"}, []any{"a", "b"}},
		{"nil slice", []string(nil), nil},
		{"byte array", [2]byte{1, 2}, []byte{1, 2}},
		{"typed map", map[string]int{"k": 1}, map[string]any{"k": 1}},
		{"nil map", map[string]any(nil), nil},
		{"optional", optional.Some("v"), "v"},
		{"empty optional", optional.None[string](), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, evaluator.StoredValue(tt.in, nil, nil))
			require.Equal(t, tt.want, evaluator.StoredValue(tt.in, nil, &units{left: 1 << 20}))
		})
	}

	// A conversion the budget refuses returns the value unchanged.
	big := make([]string, 64)
	b := &units{left: 3}
	require.Equal(t, big, evaluator.StoredValue(big, nil, b))
	require.Negative(t, b.left)

	// Values already stored are returned as they are, without a charge.
	b = &units{left: 0}
	doc := map[string]any{"k": []any{1}}
	require.Equal(t, doc, evaluator.StoredValue(doc, nil, b))
	require.Zero(t, b.left)
}

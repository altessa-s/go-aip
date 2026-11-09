// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

func TestFieldKind_IsNumeric(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind filter.FieldKind
		want bool
	}{
		{filter.FieldKindUnspecified, false},
		{filter.FieldKindInt, true},
		{filter.FieldKindFloat, true},
		{filter.FieldKindDecimal, true},
		{filter.FieldKindString, false},
		{filter.FieldKindBool, false},
		{filter.FieldKindBytes, false},
		{filter.FieldKindTimestamp, false},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.kind.IsNumeric())
		})
	}
}

func TestKindsComparable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		a, b filter.FieldKind
		want bool
	}{
		{"unspecified left", filter.FieldKindUnspecified, filter.FieldKindString, true},
		{"unspecified right", filter.FieldKindBool, filter.FieldKindUnspecified, true},
		{"equal", filter.FieldKindTimestamp, filter.FieldKindTimestamp, true},
		{"int float", filter.FieldKindInt, filter.FieldKindFloat, true},
		{"decimal int", filter.FieldKindDecimal, filter.FieldKindInt, true},
		{"string int", filter.FieldKindString, filter.FieldKindInt, false},
		{"bytes string", filter.FieldKindBytes, filter.FieldKindString, false},
		{"bool timestamp", filter.FieldKindBool, filter.FieldKindTimestamp, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, filter.KindsComparable(tc.a, tc.b))
			require.Equal(t, tc.want, filter.KindsComparable(tc.b, tc.a))
		})
	}
}

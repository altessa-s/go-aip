// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing/evaluation"
)

// TestNonFiniteStoredFloat pins the error of a stored NaN or infinity: it wraps ErrNonFiniteFloat and keeps its message.
func TestNonFiniteStoredFloat(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)

	tests := []struct {
		name  string
		expr  string
		data  map[string]any
		field string
	}{
		{name: "scalar infinity", expr: `score == 1.0`, data: coverageRecord(map[string]any{"score": math.Inf(-1)}), field: "score"},
		{name: "scalar NaN", expr: `score < 1.0`, data: coverageRecord(map[string]any{"score": math.NaN()}), field: "score"},
		{name: "optional infinity", expr: `optFloat == 1.0`, data: coverageRecord(map[string]any{"optFloat": math.Inf(1)}), field: "optFloat"},
		{name: "array element", expr: `floats == 1.0`, data: coverageRecord(map[string]any{"floats": []float64{1, math.Inf(1)}}), field: "floats"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runProgram(t, profile, anyFieldPolicy(t), rawParser(t, tt.expr), tt.data)
			require.ErrorIs(t, err, evaluation.ErrNonFiniteFloat)
			require.EqualError(t, err, `field "`+tt.field+`" contains a non-finite float`)
		})
	}
}

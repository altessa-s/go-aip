// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/internal/testhelpers"
)

func TestNewTelemetry(t *testing.T) {
	t.Parallel()

	var disabled *filter.Telemetry
	disabled.Start()()
	require.Nil(t, filter.NewTelemetry(nil, "x"))

	m := testhelpers.NewRecordingMeter()
	filter.NewTelemetry(m, "x").Start()()
	require.Equal(t, map[string]int{"filter_x_translate_duration_seconds": 1}, m.Counts())
	require.Equal(t, map[string]string{"filter_x_translate_duration_seconds": "Duration of filter translation."}, m.Help())
}

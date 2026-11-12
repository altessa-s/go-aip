// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/testhelpers"
	"github.com/altessa-s/go-aip/projection"

	metrics "github.com/altessa-s/go-aip/metrics"
)

func TestNewTelemetry(t *testing.T) {
	t.Parallel()

	var disabled *metrics.TranslateTimer
	disabled.Start()()
	require.Nil(t, projection.NewTelemetry(nil, "x"))

	m := testhelpers.NewRecordingMeter()
	projection.NewTelemetry(m, "x").Start()()
	require.Equal(t, map[string]int{"projection_x_translate_duration_seconds": 1}, m.Counts())
	require.Equal(t, map[string]string{"projection_x_translate_duration_seconds": "Duration of projection translation in seconds."}, m.Help())
}

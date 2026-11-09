// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/testhelpers"
	"github.com/altessa-s/go-aip/metrics"
)

func TestTranslateTimer(t *testing.T) {
	t.Parallel()

	var disabled *metrics.TranslateTimer
	disabled.Start()()
	require.Nil(t, metrics.NewTranslateTimer(nil, "filter_x", "help"))

	m := testhelpers.NewRecordingMeter()
	timer := metrics.NewTranslateTimer(m, "filter_x", "Duration of filter translation.")
	timer.Start()()
	timer.Start()()

	require.Equal(t, map[string]int{"filter_x_translate_duration_seconds": 2}, m.Counts())
	require.Equal(t, map[string]string{"filter_x_translate_duration_seconds": "Duration of filter translation."}, m.Help())
}

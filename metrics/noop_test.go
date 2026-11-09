// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/metrics"
)

func TestNoop(t *testing.T) {
	t.Parallel()

	m := metrics.Noop()
	require.Same(t, m, metrics.Noop(), "Noop is a singleton")
	require.Same(t, m, m.WithSubsystem("filter"), "WithSubsystem returns the no-op meter")

	opts := metrics.MetricOpts{Name: "parse_errors_total", Help: "help"}
	hopts := metrics.HistogramOpts{Name: "parse_duration_seconds"}

	for _, c := range []metrics.Counter{m.Counter(opts), m.MustCounter(opts)} {
		require.NotNil(t, c)
		c.Inc()
		c.Add(2)
		c.Add(-1)
	}

	for _, tm := range []metrics.Timer{m.Timer(hopts), m.MustTimer(hopts)} {
		require.NotNil(t, tm)

		stop := tm.Start()
		require.NotNil(t, stop)
		stop()
		tm.ObserveDuration(time.Second)
	}
}

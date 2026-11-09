// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"time"

	"github.com/altessa-s/go-aip/metrics"
)

// telemetrySubsystem scopes the evaluator's metrics as the filter translators scope theirs ("filter_" + backend).
const telemetrySubsystem = "filter_evaluator"

// telemetry records evaluation and preparation durations ([WithCollector]). A nil receiver is disabled.
type telemetry struct {
	evaluate metrics.Timer
	prepare  metrics.Timer
}

// newTelemetry creates the evaluator's metrics in m. A nil meter disables collection.
func newTelemetry(m metrics.Meter) *telemetry {
	if m == nil {
		return nil
	}

	m = m.WithSubsystem(telemetrySubsystem)

	return &telemetry{
		evaluate: m.Timer(metrics.HistogramOpts{Name: "evaluate_duration_seconds",
			Help: "Duration of filter evaluation over one value."}),
		prepare: m.Timer(metrics.HistogramOpts{Name: "prepare_duration_seconds",
			Help: "Duration of filter preparation."}),
	}
}

// observeSince records the time elapsed since start in t.
func observeSince(t metrics.Timer, start time.Time) { t.ObserveDuration(time.Since(start)) }

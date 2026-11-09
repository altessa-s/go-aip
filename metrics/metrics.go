// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics

import "time"

// Meter creates metric instruments. It is the narrow handle instrumented
// packages accept: it carries no lifecycle, so a component holding a Meter
// cannot shut down or flush the backend behind it. All methods are safe for
// concurrent use.
type Meter interface {
	// Counter creates or retrieves a counter metric.
	Counter(opts MetricOpts) Counter
	// Timer creates or retrieves a timer metric (histogram-based, in seconds).
	Timer(opts HistogramOpts) Timer
	// MustCounter creates a counter, panicking on error.
	MustCounter(opts MetricOpts) Counter
	// MustTimer creates a timer, panicking on error.
	MustTimer(opts HistogramOpts) Timer
	// WithSubsystem returns a Meter with the given subsystem, added between
	// the namespace and the metric name: {namespace}_{subsystem}_{name}.
	WithSubsystem(subsystem string) Meter
}

// Counter is a metric that can only increase. All methods are safe for
// concurrent use.
type Counter interface {
	// Inc increments the counter by 1.
	Inc()
	// Add increments the counter by the given delta; a negative delta is
	// ignored.
	Add(delta float64)
}

// Timer measures durations into a histogram of seconds. All methods are safe
// for concurrent use.
type Timer interface {
	// Start begins timing and returns the function that records the elapsed
	// duration when called.
	Start() (stop func())
	// ObserveDuration records a duration directly.
	ObserveDuration(d time.Duration)
}

// MetricOpts holds the options of a [Counter].
type MetricOpts struct {
	// Name is the metric name (e.g. "parse_errors_total"). Required.
	Name string
	// Help is a human-readable description of the metric.
	Help string
}

// HistogramOpts holds the options of a [Timer]. The bucket boundaries are
// left to the backend.
type HistogramOpts struct {
	MetricOpts
}

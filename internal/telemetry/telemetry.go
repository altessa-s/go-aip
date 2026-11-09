// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package telemetry

import "github.com/altessa-s/go-aip/metrics"

// Parser holds the metrics of one parser: the duration of every parse and the number of failed ones.
type Parser struct {
	// Duration is the parse_duration_seconds timer.
	Duration metrics.Timer
	// Errors is the parse_errors_total counter.
	Errors metrics.Counter
}

// NewParser creates the parse metrics under subsystem; noun names what is parsed in the help texts ("Duration of
// <noun> parse operations in seconds."). A nil meter uses [metrics.Noop]. The instruments are created with the Must
// methods, so a meter that cannot create them panics at construction.
func NewParser(m metrics.Meter, subsystem, noun string) Parser {
	if m == nil {
		m = metrics.Noop()
	}

	scoped := m.WithSubsystem(subsystem)

	return Parser{
		Duration: scoped.MustTimer(metrics.HistogramOpts{
			Name: "parse_duration_seconds",
			Help: "Duration of " + noun + " parse operations in seconds.",
		}),
		Errors: scoped.MustCounter(metrics.MetricOpts{
			Name: "parse_errors_total",
			Help: "Total number of " + noun + " parse failures.",
		}),
	}
}

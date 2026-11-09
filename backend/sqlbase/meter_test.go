// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"maps"
	"sync"
	"time"

	"github.com/altessa-s/go-aip/metrics"
)

// countingMeter is a [metrics.Meter] that counts, by full name ({subsystem}_{name}), how often each timer completed and
// each counter was incremented. It is safe for concurrent use; create it with [newCountingMeter].
type countingMeter struct {
	state     *meterCounts
	subsystem string
}

type meterCounts struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingMeter() countingMeter {
	return countingMeter{state: &meterCounts{counts: map[string]int{}}}
}

// Counts returns a copy of the counts by full metric name.
func (m countingMeter) Counts() map[string]int {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	return maps.Clone(m.state.counts)
}

func (m countingMeter) Counter(o metrics.MetricOpts) metrics.Counter {
	return countingInstrument{m.record(o.Name)}
}

func (m countingMeter) MustCounter(o metrics.MetricOpts) metrics.Counter { return m.Counter(o) }

func (m countingMeter) Timer(o metrics.HistogramOpts) metrics.Timer {
	return countingInstrument{m.record(o.Name)}
}

func (m countingMeter) MustTimer(o metrics.HistogramOpts) metrics.Timer { return m.Timer(o) }

func (m countingMeter) WithSubsystem(s string) metrics.Meter {
	m.subsystem = s

	return m
}

// record returns the function adding one to the count of the metric name.
func (m countingMeter) record(name string) func() {
	full := name
	if m.subsystem != "" {
		full = m.subsystem + "_" + name
	}

	return func() {
		m.state.mu.Lock()
		defer m.state.mu.Unlock()

		m.state.counts[full]++
	}
}

// countingInstrument is the Counter and Timer of [countingMeter]: every increment and every completed timing adds one.
type countingInstrument struct{ record func() }

func (c countingInstrument) Inc()                          { c.record() }
func (c countingInstrument) Add(float64)                   { c.record() }
func (c countingInstrument) Start() func()                 { return c.record }
func (c countingInstrument) ObserveDuration(time.Duration) { c.record() }

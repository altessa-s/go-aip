// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package testhelpers

import (
	"maps"
	"sync"
	"time"

	"github.com/altessa-s/go-aip/metrics"
)

// RecordingMeter is a [metrics.Meter] that records, by full name
// ({subsystem}_{name}), how often each timer completed and each counter was
// incremented, and the help text of every instrument. It is safe for
// concurrent use; create it with [NewRecordingMeter].
type RecordingMeter struct {
	state     *meterState
	subsystem string
}

type meterState struct {
	mu     sync.Mutex
	counts map[string]int
	help   map[string]string
}

// NewRecordingMeter returns an empty [RecordingMeter].
func NewRecordingMeter() RecordingMeter {
	return RecordingMeter{state: &meterState{counts: map[string]int{}, help: map[string]string{}}}
}

// Counts returns a copy of the recorded counts by full metric name.
func (m RecordingMeter) Counts() map[string]int {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	return maps.Clone(m.state.counts)
}

// Help returns a copy of the help texts by full metric name.
func (m RecordingMeter) Help() map[string]string {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	return maps.Clone(m.state.help)
}

// Counter implements [metrics.Meter].
func (m RecordingMeter) Counter(o metrics.MetricOpts) metrics.Counter {
	return recordingInstrument{m.register(o.Name, o.Help)}
}

// MustCounter implements [metrics.Meter].
func (m RecordingMeter) MustCounter(o metrics.MetricOpts) metrics.Counter { return m.Counter(o) }

// Timer implements [metrics.Meter].
func (m RecordingMeter) Timer(o metrics.HistogramOpts) metrics.Timer {
	return recordingInstrument{m.register(o.Name, o.Help)}
}

// MustTimer implements [metrics.Meter].
func (m RecordingMeter) MustTimer(o metrics.HistogramOpts) metrics.Timer { return m.Timer(o) }

// WithSubsystem implements [metrics.Meter].
func (m RecordingMeter) WithSubsystem(s string) metrics.Meter {
	m.subsystem = s

	return m
}

func (m RecordingMeter) register(name, help string) func() {
	full := name
	if m.subsystem != "" {
		full = m.subsystem + "_" + name
	}

	m.state.mu.Lock()
	m.state.help[full] = help
	m.state.mu.Unlock()

	return func() {
		m.state.mu.Lock()
		defer m.state.mu.Unlock()

		m.state.counts[full]++
	}
}

// recordingInstrument is the Counter and Timer of [RecordingMeter]: every
// increment and every completed timing adds one.
type recordingInstrument struct{ record func() }

func (r recordingInstrument) Inc()                          { r.record() }
func (r recordingInstrument) Add(float64)                   { r.record() }
func (r recordingInstrument) Start() func()                 { return r.record }
func (r recordingInstrument) ObserveDuration(time.Duration) { r.record() }

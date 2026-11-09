// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics

import "time"

// noopMeter is the [Meter] returned by [Noop].
type noopMeter struct{}

var noopInstance = &noopMeter{}

// Noop returns a [Meter] whose instruments record nothing. The returned value
// is a singleton, and its [Meter.WithSubsystem] returns it again.
func Noop() Meter { return noopInstance }

// Counter implements [Meter].
func (*noopMeter) Counter(MetricOpts) Counter { return noopCounter{} }

// Timer implements [Meter].
func (*noopMeter) Timer(HistogramOpts) Timer { return noopTimer{} }

// MustCounter implements [Meter].
func (*noopMeter) MustCounter(MetricOpts) Counter { return noopCounter{} }

// MustTimer implements [Meter].
func (*noopMeter) MustTimer(HistogramOpts) Timer { return noopTimer{} }

// WithSubsystem implements [Meter].
func (n *noopMeter) WithSubsystem(string) Meter { return n }

// noopCounter is the [Counter] of [Noop].
type noopCounter struct{}

func (noopCounter) Inc()        {}
func (noopCounter) Add(float64) {}

// noopTimer is the [Timer] of [Noop].
type noopTimer struct{}

func (noopTimer) Start() func()                 { return func() {} }
func (noopTimer) ObserveDuration(time.Duration) {}

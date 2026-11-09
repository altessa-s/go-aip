// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

var _ filter.Compatibility = (*evaluator.Evaluator)(nil)

// recordingMeter counts the observations of each timer by its full name.
type recordingMeter struct {
	subsystem string
	mu        *sync.Mutex
	counts    map[string]int
}

func newRecordingMeter() *recordingMeter {
	return &recordingMeter{mu: new(sync.Mutex), counts: map[string]int{}}
}

func (m *recordingMeter) Counter(metrics.MetricOpts) metrics.Counter {
	return metrics.Noop().Counter(metrics.MetricOpts{})
}

func (m *recordingMeter) MustCounter(metrics.MetricOpts) metrics.Counter {
	return m.Counter(metrics.MetricOpts{})
}

func (m *recordingMeter) MustTimer(o metrics.HistogramOpts) metrics.Timer {
	return m.Timer(o)
}

func (m *recordingMeter) Timer(o metrics.HistogramOpts) metrics.Timer {
	return recordingTimer{meter: m, name: m.subsystem + "_" + o.Name}
}

func (m *recordingMeter) WithSubsystem(s string) metrics.Meter {
	return &recordingMeter{subsystem: s, mu: m.mu, counts: m.counts}
}

func (m *recordingMeter) count(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.counts[name]
}

type recordingTimer struct {
	meter *recordingMeter
	name  string
}

func (t recordingTimer) Start() func() { return func() { t.ObserveDuration(0) } }

func (t recordingTimer) ObserveDuration(time.Duration) {
	t.meter.mu.Lock()
	defer t.meter.mu.Unlock()

	t.meter.counts[t.name]++
}

func TestWithCollector(t *testing.T) {
	t.Parallel()

	const (
		evaluate = "filter_evaluator_evaluate_duration_seconds"
		prepare  = "filter_evaluator_prepare_duration_seconds"
	)

	tests := []struct {
		name string
		opts []evaluator.Option
	}{
		{name: "native"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meter := newRecordingMeter()
			eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("active")),
				append(tt.opts, evaluator.WithCollector(meter))...)
			require.NoError(t, err)

			node := &filter.IdentNode{Name: "active"}
			matched, err := eval.Evaluate(node, map[string]any{"active": true})
			require.NoError(t, err)
			require.True(t, matched)

			prepared, err := eval.Prepare(node)
			require.NoError(t, err)

			_, err = prepared.Evaluate(map[string]any{"active": false})
			require.NoError(t, err)

			// A failed evaluation is measured too.
			_, err = eval.Evaluate(&filter.IdentNode{Name: "secret"}, nil)
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)

			require.Equal(t, 3, meter.count(evaluate))
			require.Equal(t, 1, meter.count(prepare))
		})
	}
}

func TestWithCollectorNilDisables(t *testing.T) {
	t.Parallel()

	var meter *recordingMeter

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("active")), evaluator.WithCollector(nil),
		evaluator.WithCollector(meter))
	require.NoError(t, err)

	matched, err := eval.Evaluate(&filter.IdentNode{Name: "active"}, map[string]any{"active": true})
	require.NoError(t, err)
	require.True(t, matched)
}

func TestValidateAST(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("active")))
	require.NoError(t, err)

	require.NoError(t, eval.ValidateAST(&filter.IdentNode{Name: "active"}))
	require.ErrorIs(t, eval.ValidateAST(&filter.IdentNode{Name: "secret"}), filter.ErrFieldNotAllowed)
}

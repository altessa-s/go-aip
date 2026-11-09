// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/altessa-s/go-aip/filter/parser"
	"github.com/altessa-s/go-aip/metrics"
)

// backendOpts, backendHistogramOpts, backendCounter, backendTimer and
// backendMeter stand for the metrics package being adapted (go-atlas
// observability/metrics has this shape): its own option structs, and
// instruments that already have Inc/Add and Start/ObserveDuration.
type (
	backendOpts struct {
		Name, Help string
		LabelNames []string
	}
	backendHistogramOpts struct {
		backendOpts

		Buckets []float64
	}
)

// recorded counts what the backend's instruments observe, by full name.
type recorded struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *recorded) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counts[name]++
}

type backendCounter struct {
	r    *recorded
	name string
}

func (c backendCounter) Inc()        { c.r.add(c.name) }
func (c backendCounter) Add(float64) { c.r.add(c.name) }

type backendTimer struct {
	r    *recorded
	name string
}

func (t backendTimer) Start() func()                 { return func() { t.r.add(t.name) } }
func (t backendTimer) ObserveDuration(time.Duration) { t.r.add(t.name) }

type backendMeter struct {
	r      *recorded
	prefix string
}

func (m backendMeter) Counter(o backendOpts) backendCounter {
	return backendCounter{r: m.r, name: m.prefix + o.Name}
}

func (m backendMeter) Timer(o backendHistogramOpts) backendTimer {
	return backendTimer{r: m.r, name: m.prefix + o.Name}
}

func (m backendMeter) WithSubsystem(s string) backendMeter {
	return backendMeter{r: m.r, prefix: m.prefix + s + "_"}
}

// meter adapts backendMeter to [metrics.Meter]: the options are converted
// field by field and WithSubsystem wraps the returned meter again.
type meter struct{ m backendMeter }

func counterOpts(o metrics.MetricOpts) backendOpts { return backendOpts{Name: o.Name, Help: o.Help} }

func timerOpts(o metrics.HistogramOpts) backendHistogramOpts {
	return backendHistogramOpts{backendOpts: counterOpts(o.MetricOpts)}
}

func (a meter) Counter(o metrics.MetricOpts) metrics.Counter     { return a.m.Counter(counterOpts(o)) }
func (a meter) Timer(o metrics.HistogramOpts) metrics.Timer      { return a.m.Timer(timerOpts(o)) }
func (a meter) MustCounter(o metrics.MetricOpts) metrics.Counter { return a.m.Counter(counterOpts(o)) }
func (a meter) MustTimer(o metrics.HistogramOpts) metrics.Timer  { return a.m.Timer(timerOpts(o)) }
func (a meter) WithSubsystem(s string) metrics.Meter             { return meter{a.m.WithSubsystem(s)} }

// Example_adapter adapts a metrics backend to [metrics.Meter] and hands it to
// the filter parser, which records a parse duration and parse errors.
func Example_adapter() {
	rec := &recorded{counts: map[string]int{}}
	backend := backendMeter{r: rec, prefix: "app_"}

	p, err := parser.New(parser.WithCollector(meter{m: backend}), parser.WithNoCache(true))
	if err != nil {
		fmt.Println(err)

		return
	}

	_, _ = p.Parse(context.Background(), `name == "x"`)
	_, _ = p.Parse(context.Background(), `name ==`)

	names := make([]string, 0, len(rec.counts))
	for name, n := range rec.counts {
		names = append(names, fmt.Sprintf("%s=%d", name, n))
	}

	sort.Strings(names)
	fmt.Println(strings.Join(names, "\n"))
	// Output:
	// app_filter_parse_duration_seconds=2
	// app_filter_parse_errors_total=1
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package metrics is the consumer-side metrics API of go-aip: the few
// instruments the parsers and translators record, declared here so the
// library depends on no metrics backend.
//
// A component that records metrics accepts a [Meter] (through its
// WithCollector option); without one it uses [Noop], whose instruments do
// nothing. The parsers of filter, orderby and projection record a parse
// duration [Timer] and a parse error [Counter]; the filter, order and
// projection translators record a translation duration [TranslateTimer]
// under filter_<backend>, orderby_<backend> or projection_<backend>. Every
// metric is created through [Meter.WithSubsystem], so its full name is
// {namespace}_{subsystem}_{name} with the namespace chosen by the backend.
//
// # Adapting a backend
//
// The method names, option structs and semantics match the subset of the
// go-atlas observability/metrics API the packages use, so an adapter over a
// go-atlas Meter (or any Prometheus or OpenTelemetry wrapper) is a thin
// forwarding type: Counter and Timer convert the options field by field,
// WithSubsystem wraps the returned meter again, and the instruments forward
// Inc, Add, Start and ObserveDuration unchanged. An implementation must be
// safe for concurrent use; Must* methods panic when an instrument cannot be
// created, the others return a usable (possibly no-op) instrument.
package metrics

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package metrics

// TranslateTimer records the duration of one translator as the histogram
// {subsystem}_translate_duration_seconds. A nil *TranslateTimer is disabled:
// its Start returns a no-op callback. It is safe for concurrent use.
//
// Translators do not create it directly: filter.NewTelemetry,
// orderby.NewTelemetry and projection.NewTelemetry name the subsystem after
// the translated parameter and the backend.
type TranslateTimer struct{ duration Timer }

// NewTranslateTimer creates the translate_duration_seconds timer under
// subsystem with the given help text. A nil meter disables collection and
// returns nil.
func NewTranslateTimer(m Meter, subsystem, help string) *TranslateTimer {
	if m == nil {
		return nil
	}

	return &TranslateTimer{duration: m.WithSubsystem(subsystem).Timer(HistogramOpts{
		Name: "translate_duration_seconds",
		Help: help,
	})}
}

// Start begins timing one translation and returns its completion callback.
func (t *TranslateTimer) Start() (stop func()) {
	if t == nil {
		return func() {}
	}

	return t.duration.Start()
}

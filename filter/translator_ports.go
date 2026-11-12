// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import "github.com/altessa-s/go-aip/metrics"

// Translator is the small port for backend translators returning one query representation.
type Translator[Out any] interface {
	Translate(Node) (Out, error)
}

// Compatibility checks the actual configured backend, including expression shape,
// field policy and literal encoding. It performs no database operations.
// Unlike a static list of operators, it accounts for operand combinations.
type Compatibility interface{ ValidateAST(Node) error }

// NewTelemetry creates the translation timer of a filter translator, recorded as the histogram
// filter_<backend>_translate_duration_seconds. A nil meter disables collection and returns nil, whose Start is a no-op.
func NewTelemetry(m metrics.Meter, backend string) *metrics.TranslateTimer {
	return metrics.NewTranslateTimer(m, "filter_"+backend, "Duration of filter translation.")
}

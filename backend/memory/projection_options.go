// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/projection"
)

//go:generate optgen generate --type=projectionOptions --option-type=ProjectionOption --output=projection_options_gen.go

type projectionOptions struct {
	// WithProjectionOptions sets the storage options of the translator: mapping, denied storage fields and required fields.
	projectionOptions []projection.TranslatorOption `optgen:"allownull" optval:"clone"`
	// WithProjectionCollector records the duration of each translation as the histogram
	// projection_memory_translate_duration_seconds; nil disables collection.
	collector metrics.Meter `opt:"ProjectionCollector" optgen:"notnil"`
}

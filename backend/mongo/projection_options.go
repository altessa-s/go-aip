// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/projection"
)

//go:generate optgen generate --type=projectionOptions --option-type=ProjectionOption --output=projection_options_gen.go

type projectionOptions struct {
	// WithProjectionOptions sets the storage options of the translator: mapping, denied storage fields and required fields.
	projectionOptions []projection.TranslatorOption `opt:"ProjectionOptions" optgen:"allownull" optval:"clone"`
	// WithProjectionIDField configures the storage identity included by WithProjectionIncludeID. Only MongoDB _id is implicitly suppressed.
	idField string `opt:"ProjectionIDField" optgen:"default=DefaultIDField"`
	// WithProjectionIncludeID includes the identity field through the required-field policy.
	includeID bool `opt:"ProjectionIncludeID" optval:"param"`
	// WithProjectionCollector records the duration of each translation; nil disables collection.
	collector metrics.Meter `opt:"ProjectionCollector" optgen:"notnil"`
}

// DefaultIDField is the MongoDB identity field included by WithProjectionIncludeID.
const DefaultIDField = "_id"

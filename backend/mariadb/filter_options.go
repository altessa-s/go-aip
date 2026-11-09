// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"
)

//go:generate optgen generate --type=filterOptions --option-type=FilterOption --output=filter_options_gen.go

type filterOptions struct {
	// decimalFormatSet is written by generated WithFilterDecimalFormat, which the schema translator refuses.
	decimalFormatSet bool `opt:"-"`
	// WithFilterOptions binds the shared policy to the storage names of this backend ([filter.WithFieldMapping]).
	filterOptions []filter.TranslatorOption `opt:"FilterOptions" optgen:"allownull" optval:"clone"`
	// WithFilterCollector records the translation duration under the backend label "mariadb". Without it nothing is
	// recorded.
	collector metrics.Meter `opt:"FilterCollector" optgen:"notnil"`
	// WithFilterLiteralEncoder converts values before backend quoting. It must be concurrency safe. Scalar translator
	// only.
	literalEncoder func(any) (any, error) `opt:"FilterLiteralEncoder"`
	// WithFilterPredicateRenderer supplies a trusted predicate override that binds client values through its ValueFunc.
	// Return handled=false to use built-ins. Scalar translator only.
	predicateRenderer sqlbase.PredicateRenderer `opt:"FilterPredicateRenderer"`
	// WithFilterDecimalFormat sets the DECIMAL(P, S) domain decimal operands are cast to. Scalar translator only: the
	// schema translator reads [WithSchemaDecimalFormat].
	decimalFormat sqlbase.DecimalFormat `opt:"FilterDecimalFormat" optgen:"default=sqlbase.DefaultDecimalFormat(),enables=decimalFormatSet"`
}

// scalarOnly reports whether o sets an option only [NewFilterTranslator] reads.
func (o *filterOptions) scalarOnly() bool {
	return o.literalEncoder != nil || o.predicateRenderer != nil || o.decimalFormatSet
}

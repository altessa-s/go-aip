// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"
)

//go:generate optgen generate --type=filterOptions --option-type=FilterOption --output=filter_options_gen.go

type filterOptions struct {
	// precisionSet is written by generated WithFilterTimestampPrecision, which the schema translator refuses.
	precisionSet bool `opt:"-"`
	// WithFilterOptions binds the shared policy to the storage names of this backend ([filter.WithFieldMapping]).
	filterOptions []filter.TranslatorOption `opt:"FilterOptions" optgen:"allownull" optval:"clone"`
	// WithFilterCollector records the translation duration under the backend label "clickhouse". Without it nothing is
	// recorded.
	collector metrics.Meter `opt:"FilterCollector" optgen:"notnil"`
	// WithFilterLiteralEncoder converts values before backend quoting. It must be concurrency safe. Scalar translator only.
	literalEncoder func(any) (any, error) `opt:"FilterLiteralEncoder"`
	// WithFilterPredicateRenderer supplies a trusted predicate override that binds client values through its ValueFunc.
	// Return handled=false to use built-ins. Scalar translator only.
	predicateRenderer sqlbase.PredicateRenderer `opt:"FilterPredicateRenderer"`
	// WithFilterTimestampPrecision sets the fractional digits of inline DateTime64 literals: the digits the precision
	// needs (1ms renders scale 3), at most nine; zero keeps nanoseconds. Scalar translator only: the schema translator
	// reads [WithSchemaTimestampPrecision].
	timestampPrecision time.Duration `opt:"FilterTimestampPrecision" optgen:"default=DefaultTimestampPrecision,enables=precisionSet" optval:"nonpositive"`
}

// scalarOnly reports whether o sets an option only [NewFilterTranslator] reads.
func (o *filterOptions) scalarOnly() bool {
	return o.literalEncoder != nil || o.predicateRenderer != nil || o.precisionSet
}

// DefaultTimestampPrecision is the default timestamp resolution, DateTime64(3).
const DefaultTimestampPrecision = time.Millisecond

// maxTimestampDigits is the finest DateTime64 precision, nanoseconds.
const maxTimestampDigits = 9

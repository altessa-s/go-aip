// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
)

//go:generate optgen generate --type=orderOptions --option-type=OrderOption --output=order_options_gen.go

// DefaultMaxDataBudget bounds the work an [evaluator.ValueEncoding] may spend reading one value of one record: one sort value
// of the [Comparator] and one field read or predicate evaluation of the [ListEngine]. It is the filter evaluator's default
// ([evaluator.DefaultMaxDataBudget]).
const DefaultMaxDataBudget = evaluator.DefaultMaxDataBudget

type orderOptions struct {
	// WithOrderOptions binds the shared policy to the storage names of this backend: [orderby.WithFieldMapping],
	// [orderby.WithKeyResolver], [orderby.WithDeniedStorageFields] and [orderby.WithTiebreaker].
	orderOptions []orderby.TranslatorOption `opt:"OrderOptions" optgen:"allownull" optval:"clone"`
	// WithOrderCollector records the translation duration as the histogram orderby_memory_translate_duration_seconds. Without it
	// nothing is recorded.
	collector metrics.Meter `opt:"OrderCollector" optgen:"notnil"`
	// WithOrderValueEncoding reads values whose stored form a storage encoding defines (a driver's own types, a struct, a value
	// encoding itself) through enc, as [evaluator.WithValueEncoding] does for the filter evaluator, so that the comparator and
	// the evaluator read a record identically. It must be safe for concurrent use.
	valueEncoding evaluator.ValueEncoding `opt:"OrderValueEncoding" optgen:"notnil"`
	// WithOrderMaxDataBudget bounds the work the value encoding may spend reading one sort value; a value it cannot read within the
	// budget is unsortable. Defaults to DefaultMaxDataBudget.
	maxDataBudget int `opt:"OrderMaxDataBudget" optgen:"default=DefaultMaxDataBudget" optval:"positive"`
}

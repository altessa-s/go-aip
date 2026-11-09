// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

// FilterTranslator converts filter AST nodes to ClickHouse SQL WHERE clauses.
//
// Translate emits `?` placeholders and returns the bound values
// alongside the clause; TranslateInline emits self-contained SQL with
// every literal rendered in place. Both run the shared SQL walker —
// see [sqlbase.FilterTranslator].
//
// A FilterTranslator is safe for concurrent Translate and TranslateInline calls.
type FilterTranslator struct {
	base *sqlbase.FilterTranslator
}

// NewFilterTranslator creates a new ClickHouse filter translator over policy
// with the given options. Returns [filter.ErrPolicyRequired] for a nil
// policy and [filter.ErrInvalidOptions] for a negative timestamp precision.
// The policy holds the field rules in CEL-side names and may be
// shared with other translators; [WithFilterOptions] maps them to storage
// names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	bound, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if o.timestampPrecision < 0 {
		return nil, filter.ErrInvalidOptions
	}

	base, err := sqlbase.NewFilterTranslator(bound, filterDialect{timestampPrecision: timestampDigits(o.timestampPrecision)},
		sqlbase.WithFilterLiteralEncoder(o.literalEncoder), sqlbase.WithFilterPredicateRenderer(o.predicateRenderer), sqlbase.WithFilterCollector(o.collector))
	if err != nil {
		return nil, err
	}

	return &FilterTranslator{base: base}, nil
}

// Translate converts n to a parameterized WHERE clause and its bound values, using isolated state, and records
// translation metrics ([sqlbase.FilterTranslator.Translate]).
func (t *FilterTranslator) Translate(n filter.Node) (string, []any, error) {
	return t.base.Translate(n)
}

// TranslateInline converts n to a self-contained WHERE clause with every literal rendered in place, using isolated state,
// and records translation metrics ([sqlbase.FilterTranslator.TranslateInline]).
func (t *FilterTranslator) TranslateInline(n filter.Node) (string, error) {
	return t.base.TranslateInline(n)
}

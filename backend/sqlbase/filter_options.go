// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import "github.com/altessa-s/go-aip/metrics"

//go:generate optgen generate --type=filterOptions --option-type=FilterOption --output=filter_options_gen.go

type filterOptions struct {
	// WithFilterLiteralEncoder converts every literal before it is bound or rendered inline. It owns precision and driver
	// representation, must reject values it cannot encode, and must be safe for concurrent use. Decimal operands reach
	// it as decimal.Decimal instead of the dialect's built-in exact rendering.
	literalEncoder func(any) (any, error) `opt:"FilterLiteralEncoder"`
	// WithFilterPredicateRenderer installs an application predicate renderer that runs after field authorization and limits.
	predicateRenderer PredicateRenderer `opt:"FilterPredicateRenderer"`
	// WithFilterCollector records the duration of Translate and TranslateInline as the histogram
	// filter_<name>_translate_duration_seconds, where name is the dialect's [NamedFilterDialect] name (filter_postgres_…,
	// filter_mariadb_…, filter_clickhouse_…; "sql" for an unnamed dialect). Without it nothing is recorded.
	collector metrics.Meter `opt:"FilterCollector" optgen:"notnil"`
}

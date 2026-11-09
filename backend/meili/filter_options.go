// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"
)

//go:generate optgen generate --type=filterOptions --option-type=FilterOption --output=filter_options_gen.go

type filterOptions struct {
	// WithFilterPredicateRenderer supplies a trusted predicate override. Return handled=false to use built-ins.
	// The callback must safely encode client values and be safe for concurrent use.
	predicateRenderer func(filter.Node) (string, bool, error) `opt:"FilterPredicateRenderer"`

	// WithFilterOptions binds the shared policy to the storage names of this backend ([filter.WithFieldMapping]).
	filterOptions []filter.TranslatorOption `opt:"FilterOptions" optgen:"allownull" optval:"clone"`
	// WithFilterCollector records the translation duration as the histogram filter_meili_translate_duration_seconds.
	// Without it nothing is recorded.
	collector metrics.Meter `opt:"FilterCollector" optgen:"notnil"`
	// WithFilterLiteralEncoder converts values before backend quoting. It must be concurrency safe.
	literalEncoder func(any) (any, error) `opt:"FilterLiteralEncoder"`
	// WithFilterServerVersion declares the Meilisearch release the filters are sent to, so operators that release lacks are
	// rejected with filter.ErrUnsupportedOperation instead of failing on the server. The zero Version (the default) is the
	// latest release. NewFilterTranslator fails with filter.ErrInvalidOptions for a version before 1.0.
	serverVersion Version `opt:"FilterServerVersion"`
	// WithFilterContainsFilter declares that the server's experimental containsFilter feature is enabled: contains() renders
	// CONTAINS (Meilisearch 1.10 or later), and on 1.11 to 1.16 startsWith() renders STARTS WITH. Off by default.
	containsFilter bool `opt:"FilterContainsFilter" optval:"param"`
}

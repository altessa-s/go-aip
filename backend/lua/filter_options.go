// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"
)

//go:generate optgen generate --type=filterOptions --option-type=FilterOption --output=filter_options_gen.go

// DefaultTableVar is the Lua variable the rendered expression reads the record from unless [WithFilterTableVar] names another.
const DefaultTableVar = "d"

type filterOptions struct {
	// WithFilterTableVar names the Lua variable that holds the record (a table) the expression tests. It is trusted application
	// configuration, emitted verbatim; an empty name keeps [DefaultTableVar].
	tableVar string `opt:"FilterTableVar" optgen:"default=DefaultTableVar"`

	// WithFilterPredicateRenderer supplies a trusted predicate override. Return handled=false to use built-ins.
	// The callback must safely encode client values and be safe for concurrent use.
	predicateRenderer func(filter.Node) (string, bool, error) `opt:"FilterPredicateRenderer"`

	// WithFilterOptions binds the shared policy to the storage names of this backend ([filter.WithFieldMapping]).
	policyBindings []filter.TranslatorOption `opt:"FilterOptions" optgen:"allownull" optval:"clone"`
	collector      metrics.Meter             `opt:"FilterCollector" optgen:"notnil"`
	// WithFilterLiteralEncoder converts values before backend quoting. It must be concurrency safe.
	literalEncoder func(any) (any, error) `opt:"FilterLiteralEncoder"`
}

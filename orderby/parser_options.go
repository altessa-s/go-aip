// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

//go:generate optgen generate --type=parserOptions --output=parser_options_gen.go --option-type=ParserOption

import (
	"github.com/altessa-s/go-aip/metrics"
)

// DefaultParserCacheSize is the default maximum number of parsed Spec
// values held in the parser's LRU cache.
const DefaultParserCacheSize = 1000

// DefaultMaxExpressionLength is the default maximum allowed length (in
// bytes) for a raw order_by string. AIP-132 order_by strings are short by
// design — this default is well above any sensible production payload and
// is meant only as a safety stop against pathologically long input.
const DefaultMaxExpressionLength = 1024

// DefaultMaxKeys is the default maximum number of sort keys accepted in a
// single order_by string.
const DefaultMaxKeys = 32

// DefaultMaxFieldPathDepth is the default maximum number of dot-separated
// segments in a single field path (e.g. "a.b.c" has depth 3).
const DefaultMaxFieldPathDepth = 8

// DefaultMaxFieldNameLength is the default maximum allowed length (in
// bytes) of a single field path including its dots.
const DefaultMaxFieldNameLength = 128

// parserOptions holds [Parser] configuration. All fields are populated by
// the generated [ParserOption] setters in parser_options_gen.go. Parser
// options are unprefixed, as in filter/parser: WithCacheSize, WithNoCache,
// WithCollector and the limits.
type parserOptions struct {
	// WithAllowDuplicateKeys accepts a field named twice in one order_by string instead of failing with ErrDuplicateKey.
	allowDuplicateKeys bool `opt:"AllowDuplicateKeys" optval:"param"`
	// WithCacheSize sets the capacity of the parsed-Spec LRU cache (DefaultParserCacheSize); a non-positive value keeps the
	// default.
	cacheSize int `opt:"CacheSize" optgen:"default=DefaultParserCacheSize" optval:"positive"`
	// WithNoCache disables the parsed-Spec cache when true.
	noCache bool `opt:"NoCache" optval:"param"`
	// WithMaxExpressionLength limits the raw order_by string in bytes (DefaultMaxExpressionLength).
	maxExpressionLength int `opt:"MaxExpressionLength" optgen:"default=DefaultMaxExpressionLength" optval:"positive"`
	// WithMaxKeys limits the number of sort keys (DefaultMaxKeys).
	maxKeys int `opt:"MaxKeys" optgen:"default=DefaultMaxKeys" optval:"positive"`
	// WithMaxFieldPathDepth limits the dot-separated segments of one field path (DefaultMaxFieldPathDepth).
	maxFieldPathDepth int `opt:"MaxFieldPathDepth" optgen:"default=DefaultMaxFieldPathDepth" optval:"positive"`
	// WithMaxFieldNameLength limits one field path in bytes, dots included (DefaultMaxFieldNameLength).
	maxFieldNameLength int `opt:"MaxFieldNameLength" optgen:"default=DefaultMaxFieldNameLength" optval:"positive"`
	// WithCaseSensitiveDirection accepts only the lowercase "asc" and "desc" directions when true.
	caseSensitiveDirection bool `opt:"CaseSensitiveDirection" optval:"param"`
	// WithAllowArrayIndexPaths accepts numeric array index segments ("items.0.price") in field paths when true.
	allowArrayIndices bool `opt:"AllowArrayIndexPaths" optval:"param"`
	// WithCollector records parse latency and errors into a metrics.Meter. Without it nothing is recorded.
	collector metrics.Meter `opt:"Collector" optgen:"notnil"`
}

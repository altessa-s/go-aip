// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

//go:generate optgen generate --type=parserOptions --output=parser_options_gen.go --option-type=ParserOption

import (
	"github.com/altessa-s/go-aip/metrics"
)

// DefaultParserCacheSize is the default maximum number of parsed Spec
// values held in the parser's LRU cache.
const DefaultParserCacheSize = 1000

// DefaultMaxExpressionLength is the default maximum allowed length (in
// bytes) of a raw fields expression. It is a safety stop against
// pathologically long input, well above any sensible field list.
const DefaultMaxExpressionLength = 4096

// DefaultMaxPaths is the default maximum number of paths accepted in a
// single expression, counted before deduplication.
const DefaultMaxPaths = 64

// DefaultMaxFieldPathDepth is the default maximum number of dot-separated
// segments in a single path (e.g. "a.b.c" has depth 3).
const DefaultMaxFieldPathDepth = 8

// DefaultMaxFieldNameLength is the default maximum allowed length (in
// bytes) of a single path including its dots.
const DefaultMaxFieldNameLength = 128

// parserOptions holds [Parser] configuration. All fields are populated by
// the generated [ParserOption] setters in parser_options_gen.go. Parser
// options are unprefixed, as in filter/parser: WithCacheSize, WithNoCache,
// WithCollector and the limits.
type parserOptions struct {
	// WithPathGrammar replaces the dotted path grammar the parser accepts. Use the same PathValidator as the policy's
	// WithPathValidator (for example a protopath validator), so a path the parser admits is one the policy can check.
	pathValidator PathValidator `opt:"PathGrammar" optgen:"default=checkPath"`
	// WithRejectDuplicates fails a read mask naming a path twice with ErrDuplicatePath instead of dropping the repeat.
	rejectDuplicates bool `opt:"RejectDuplicates" optval:"param"`

	// WithCacheSize sets the capacity of the parsed-Spec LRU cache (DefaultParserCacheSize); a non-positive value keeps the
	// default.
	cacheSize int `opt:"CacheSize" optgen:"default=DefaultParserCacheSize" optval:"positive"`
	// WithNoCache disables the parsed-Spec cache when true.
	noCache bool `opt:"NoCache" optval:"param"`
	// WithMaxExpressionLength limits the raw read mask in bytes (DefaultMaxExpressionLength).
	maxExpressionLength int `opt:"MaxExpressionLength" optgen:"default=DefaultMaxExpressionLength" optval:"positive"`
	// WithMaxPaths limits the number of paths in one read mask (DefaultMaxPaths).
	maxPaths int `opt:"MaxPaths" optgen:"default=DefaultMaxPaths" optval:"positive"`
	// WithMaxFieldPathDepth limits the dot-separated segments of one path (DefaultMaxFieldPathDepth).
	maxFieldPathDepth int `opt:"MaxFieldPathDepth" optgen:"default=DefaultMaxFieldPathDepth" optval:"positive"`
	// WithMaxFieldNameLength limits one path in bytes, dots included (DefaultMaxFieldNameLength).
	maxFieldNameLength int `opt:"MaxFieldNameLength" optgen:"default=DefaultMaxFieldNameLength" optval:"positive"`
	// WithCollector records parse latency and errors into a metrics.Meter. Without it nothing is recorded.
	collector metrics.Meter `opt:"Collector" optgen:"notnil"`
}

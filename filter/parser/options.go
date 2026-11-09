// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"time"

	"cel.dev/cel-go/cel"

	"github.com/altessa-s/go-aip/metrics"

	filter "github.com/altessa-s/go-aip/filter"
)

//go:generate optgen generate --type=options --output=options_gen.go --option-type=Option

// DefaultMaxExpressionLength is the maximum allowed length (in bytes) for a CEL expression.
// Prevents excessive memory usage during parsing and LRU cache pollution.
const DefaultMaxExpressionLength = 4096

// DefaultCacheSize is the maximum number of parsed expressions to cache.
const DefaultCacheSize = 1000

// DefaultCacheBytes is the default bound, in approximate bytes of live heap, on the parsed ASTs one parser caches. A
// 4,096-byte expression can parse to an AST of about 100 KB, so an entry count alone would let a client pin around
// 100 MB per parser with distinct filters; the byte bound caps that at 8 MiB.
const DefaultCacheBytes = 8 << 20

// options holds [Parser] configuration populated by generated [Option]
// setters in options_gen.go.
type options struct {
	// WithOrderedEnums permits ordered comparisons of enum stored values.
	orderedEnums bool `optval:"param"`
	cacheSize    int  `opt:"CacheSize" optgen:"default=DefaultCacheSize" optval:"positive"`
	// WithCacheBytes bounds the approximate bytes of the cached ASTs. An AST above a sixteenth of it is returned but not
	// cached, so one huge filter cannot flush the cache.
	cacheBytes          int                              `opt:"CacheBytes" optgen:"default=DefaultCacheBytes" optval:"positive"`
	maxExpressionLength int                              `opt:"MaxExpressionLength" optgen:"default=DefaultMaxExpressionLength" optval:"positive"`
	noCache             bool                             `opt:"NoCache" optval:"param"`
	collector           metrics.Meter                    `opt:"Collector"`
	customFunctions     map[string]filter.CustomFunction //nolint:unused // populated via WithCustomFunctions generated option
	// WithAllowedFunctions restricts which CEL functions the parser
	// accepts. The whitelist covers built-in named functions (contains,
	// startsWith, endsWith, matches, size, timestamp) and any handlers
	// registered via [WithCustomFunctions]. Operators (==, !=, <, &&, ||,
	// !, in) and the has() macro are baseline grammar and are always
	// allowed.
	//
	// When this option is not set, every function reachable from the
	// parser is accepted (the historical default). A name registered as a
	// custom function but absent from the whitelist is rejected at parse
	// time with [filter.ErrFunctionNotAllowed], not at [New] — that keeps
	// explicit per-parser narrowing available.
	//
	// Example — only allow safe substring queries plus a custom shortcut:
	//
	//	parser, _ := filterparser.New(
	//	    filterparser.WithAllowedFunctions("contains", "startsWith", "createdAfter"),
	//	    filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
	//	        "createdAfter": functions.CompareField("createdAt", filter.OpGT),
	//	    }),
	//	)
	allowedFunctions map[string]struct{} `opt:"AllowedFunctions" optgen:"set"`
	// WithEnvironmentOptions extends the instance CEL environment, including macros.
	environmentOptions []cel.EnvOption `optgen:"append"`
	// WithTimestampParser replaces timestamp literal decoding.
	timestampParser func(string) (time.Time, error) `optgen:"default=parseTimestamp"`
	// WithAllowBuiltinOverride allows explicit custom handlers to replace built-in named functions.
	allowBuiltinOverride bool `optval:"param"`
	// enumNames (WithEnumNames) declares enum fields spelled by their value names: field (CEL-side name) → value name → stored
	// value, a string or an integer. See the package documentation, "Enum names".
	enumNames map[string]map[string]any
}

func parseTimestamp(value string) (time.Time, error) { return time.Parse(time.RFC3339, value) }

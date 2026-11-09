// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/fieldpolicy"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
)

//go:generate optgen generate --option-type=PolicyOption

// DefaultMaxDepth is the default maximum expression nesting depth.
const DefaultMaxDepth = 20

// DefaultMaxRegexLength is the maximum allowed length for user-provided regex patterns.
// Prevents excessive CPU and memory usage from complex regex evaluation.
const DefaultMaxRegexLength = 1024

// DefaultMaxOperations is the maximum number of AST node visits per evaluation or translation.
// Prevents DoS via wide expressions (e.g., 100 OR-ed conditions at depth 1).
const DefaultMaxOperations = 1000

// options holds the policy settings: what a client may ask for, in CEL-side (API) names, and the validation limits of a
// parsed expression. NewPolicy validates them and compiles immutable field lookups; callers cannot apply options to an
// existing Policy. Storage names belong to the translator layer ([TranslatorOption]).
type options struct {
	// WithFieldAccess adds a shared field authorization policy. Existing allow-lists remain restrictive.
	fieldAccess fieldpolicy.Access `opt:"FieldAccess" optgen:"notnil"`

	// WithDecimalParser replaces decimal conversion and resource/precision bounds.
	decimalParser func(Node, any) (decimal.Decimal, error) `optgen:"default=DecimalOperand"`

	// WithContractRules overrides individual portable validation rules. Unspecified rules follow WithPortableContract.
	contractRules *coremaps.ImmutableMap[ContractRule, bool] `optfn:"ContractRules=coremaps.NewImmutableMap[ContractRule, bool]"`
	// WithValueCodecs registers literal validation for application field kinds. Codecs must be immutable and concurrency safe.
	valueCodecs *coremaps.ImmutableMap[FieldKind, ValueCodec] `optfn:"ValueCodecs=coremaps.NewImmutableMap[FieldKind, ValueCodec]"`

	// WithRegexCacheSize bounds the instance cache; zero disables it.
	regexCacheSize int `optgen:"default=DefaultRegexCacheSize"`
	// WithRegexCompiler injects a concurrency-safe compiler. Its implementation owns any cache it needs.
	regexCompiler func(string) (RegexMatcher, error)
	// WithRegexValidator replaces the default safe-subset validator.
	regexValidator func(string) error

	// Set by WithAllowedFields: its variadic names are collected into a membership map before freezing it.
	allowedFields *coremaps.ImmutableMap[string, struct{}] `optfn:"AllowedFields=freezeFieldSet"`
	// fieldMapping maps CEL-side names to storage names. It is not a policy option: [NewTranslatorContext] sets it on the
	// storage-bound copy a translator or evaluator works with ([WithFieldMapping]).
	fieldMapping *coremaps.ImmutableMap[string, string] `opt:"-"`
	// Set by WithFieldTypes: the generated setter freezes the caller's declarations into an immutable map.
	fieldTypes *coremaps.ImmutableMap[string, FieldKind] `optfn:"FieldTypes=coremaps.NewImmutableMap[string, FieldKind]"`
	// Set by WithEnumValues: nested slices must be collected into membership maps before freezing them.
	enumValues *coremaps.ImmutableMap[string, *coremaps.ImmutableMap[int64, struct{}]] `optfn:"EnumValues=freezeEnumValues"`
	// Set by WithArrayFields: its variadic names are collected into a membership map before freezing it.
	arrayFields *coremaps.ImmutableMap[string, struct{}] `optfn:"ArrayFields=freezeFieldSet"`
	// fields is built from the options declared per field in NewPolicy.
	fields         *coremaps.ImmutableMap[string, FieldPolicy] `opt:"-"`
	maxDepth       int                                         `optgen:"default=DefaultMaxDepth" optval:"positive"`
	maxRegexLength int                                         `optgen:"default=DefaultMaxRegexLength" optval:"positive"`
	maxOperations  int                                         `optgen:"default=DefaultMaxOperations" optval:"positive"`
	untrustedInput bool                                        `optval:"param"`
	// anyField (WithAnyField) lets a translator or evaluator be built without an allow-list, accepting a filter over any field. It is
	// an explicit opt-out for filters the service builds itself: without it [NewPolicy] fails with
	// [ErrAllowlistRequired] when no non-empty [WithAllowedFields] is given. An allow-list, when given, still applies, and
	// [WithUntrustedInput] always requires one.
	anyField bool `optval:"param"`
	// safeRegex (WithSafeRegex) admits matches() under [WithUntrustedInput], which otherwise rejects it with
	// [ErrFunctionNotAllowed]: a client regex runs on the database's backtracking engine (MongoDB PCRE, PostgreSQL ~, MariaDB
	// REGEXP), where a pattern such as (a|aa)+$ takes exponential time. The admitted patterns are the safe subset every matches()
	// pattern is held to (see unsafeRegex). Without WithUntrustedInput it has no effect.
	safeRegex bool `optval:"param"`
	// unsafeRegex (WithUnsafeRegex) lifts the safe subset a matches() pattern is otherwise held to, for a service whose patterns
	// do not come from clients. A safe pattern repeats (*, +, ?, {n,m}) only a single character, a character class or the
	// any-character dot, never a parenthesized group (not even one Go folds into a class, such as (?:a|b)*), an alternation or
	// another repetition; it has at most 2 unbounded repetitions (*, +, {n,}) and
	// 4 repetitions of a variable count in all, no counted bound above 100,
	// alternations and bounded variable repetitions whose choices (the branches of each alternation, counted as written even
	// where Go folds (a|a) into one character; m-n+1, 2 for ?) multiply to at most 1000, and no two unbounded
	// repetitions in a row (with only optional or empty-width parts between) whose characters overlap, such as .*.* — a
	// backtracking engine runs such a pattern in time polynomial in the input, of a bounded degree. It also spells out what the
	// engines read differently: every literal { is escaped and no octal escape is written \1 to \7. Any other pattern fails with
	// [ErrInvalidRegex]. A literal pattern, such as the one functions.TextSearch builds, is always safe. [NewPolicy] fails with
	// [ErrInvalidOptions] when it is combined with [WithUntrustedInput].
	unsafeRegex bool `optval:"param"`
	// portableContract (WithPortableContract) makes [Validate] enforce the rules every listing backend reads alike: bare
	// identifiers are scalar bool fields; no null in an in list or as an operand of <, <=, > or >=; no empty in list; and
	// matches() patterns are limited to regexp.QuoteMeta literals, ^, $ and .*, with only a leading (?i) and only on an ASCII
	// pattern. Array null and has() translation also use listing empty/non-empty rules. A violation is [ErrInvalidExpression],
	// [ErrFieldTypeMismatch] or [ErrUnsupportedOperation].
	portableContract bool `optval:"param"`
}

// freezeFieldSet captures field names before the option can be reused.
func freezeFieldSet(fields ...string) *coremaps.ImmutableMap[string, struct{}] {
	m := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		m[field] = struct{}{}
	}

	return coremaps.NewImmutableMap(m)
}

func freezeEnumValues(values map[string][]int64) *coremaps.ImmutableMap[string, *coremaps.ImmutableMap[int64, struct{}]] {
	sets := make(map[string]*coremaps.ImmutableMap[int64, struct{}], len(values))
	for field, allowed := range values {
		set := make(map[int64]struct{}, len(allowed))
		for _, value := range allowed {
			set[value] = struct{}{}
		}

		sets[field] = coremaps.NewImmutableMap(set)
	}

	return coremaps.NewImmutableMap(sets)
}

// DefaultRegexCacheSize bounds the default per-policy regex cache.
const DefaultRegexCacheSize = 256

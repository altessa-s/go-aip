// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"time"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
)

//go:generate optgen generate --type=profileOptions --output=profile_options_gen.go --option-type=ProfileOption

// DefaultContractID identifies the portable proto3 listing contract.
const DefaultContractID = "portable-v1"

// Defaults of a [Schema].
const (
	// DefaultPageSize is the page size of a request without one (AIP-158).
	DefaultPageSize = 50
	// DefaultMaxPageSize is the largest page size; a larger request is
	// clamped to it.
	DefaultMaxPageSize = 500
	// DefaultMaxFilterDepth bounds the nesting of a filter expression.
	DefaultMaxFilterDepth = 12
	// DefaultMaxFilterOperations bounds the AST nodes a filter may visit.
	DefaultMaxFilterOperations = 128
)

// profileOptions holds the optional settings of a [Schema]. The filter fields
// and the sort patterns are positional arguments to [NewSchema].
type profileOptions struct {
	// fieldNameValidator checks public field names; nil disables syntax checks.
	// Custom syntax also requires matching filter and order parsers.
	fieldNameValidator func(string) error `optgen:"default=defaultFieldNameValidator"`
	// filterOptions override the metadata-derived filter policy, in order, for Schema and engines.
	filterOptions []filter.PolicyOption `optgen:"append" optval:"clone=eager"`
	// serverOnlyFields names declared fields reserved for server predicates and rejected in the client filter.
	serverOnlyFields []string `optval:"clone=eager,notrim,nodup"`
	// builtinFunctions replaces the default CEL builtin allowlist; an empty slice disables builtins.
	builtinFunctions []string `optgen:"default=defaultBuiltinFunctions()" optfn:"BuiltinFunctions=copyBuiltinFunctions"`
	// caseInsensitiveContainsFields selects exact public text fields for Unicode 15.1 simple-lowercase contains.
	caseInsensitiveContainsFields []string `optval:"clone=eager,notrim,nodup"`
	// timestampPrecision truncates filter literals and defaults; zero preserves nanoseconds.
	timestampPrecision time.Duration `optgen:"default=TimestampPrecision" optfn:"TimestampPrecision=timestampPrecision"`
	// emptyFieldsAllowed permits a schema without filter fields. Configure filterOptions for its policy.
	emptyFieldsAllowed bool `optval:"param"`
	// emptyOrderAllowed permits empty declarations and an ID-only default order.
	emptyOrderAllowed bool `optval:"param"`
	// unrestrictedOrder accepts any parsed order whose fields pass sortFieldValidator.
	unrestrictedOrder bool `optval:"param"`
	// sortFieldValidator replaces scalar/presence restrictions; nil disables them. Hidden-field checks remain separate.
	sortFieldValidator func(Field) error `optgen:"default=defaultSortFieldValidator"`
	// sizeValidator replaces the size comparison contract; nil disables it.
	sizeValidator func(filter.Node, *Schema) error `optgen:"default=checkSize"`
	// presenceValidator replaces required-field null/has checks; nil disables them.
	presenceValidator func(filter.Node, *Schema) error `optgen:"default=checkPresence"`
	// callValidator replaces the string-function target check; nil disables it.
	callValidator func(*filter.CallNode) error `optgen:"default=checkGlobalCall"`
	// defaultLiteral replaces default equality conversion, including array and enum rules.
	// Timestamp results are normalized using timestampPrecision after conversion.
	defaultLiteral func(Field, any) (any, error) `optgen:"default=defaultLiteral"`
	// errorFormatter formats and classifies request errors. It must return a non-nil error and be concurrency safe.
	errorFormatter func(field, reason string, cause error) error `optgen:"default=fieldError"`
	// pageSizeResolver replaces page-size defaults, limits and validation when non-nil.
	pageSizeResolver func(int32, int, int) (int, error)

	// contractID versions parser and semantic behavior in page-token bindings.
	contractID string `optgen:"default=DefaultContractID"`
	// filterParser replaces parsing. It must preserve the declared enum representation.
	filterParser FilterParser
	// orderParser replaces order syntax parsing while retaining declared pattern validation.
	orderParser OrderParser
	// filterParserOptions configure the default parser before schema-owned vocabulary and function settings.
	filterParserOptions []filterparser.Option `optgen:"append" optval:"clone=eager"`
	// orderParserOptions configure the default order parser.
	orderParserOptions []orderby.ParserOption `optgen:"append" optval:"clone=eager"`

	// defaultOrderBy is the order of a request without orderBy, in public
	// names; it must be a declared pattern or its reversal. Empty uses the
	// first declared pattern.
	defaultOrderBy string
	// functions are custom CEL functions of the filter (e.g. a free-text
	// search()), expanded by the parser into ordinary predicates over the
	// declared fields; the allowlist applies to the expansion.
	functions *coremaps.ImmutableMap[string, filter.CustomFunction] `optgen:"default=functionMap(nil)" optfn:"Functions=functionMap"`
	// parentRequired rejects a [Request] without a parent: the list is a
	// child list addressed by its parent id.
	parentRequired bool `optval:"param"`
	// defaultEqualities restrict every request whose filter does not refer
	// to a field to field == value (public name -> literal; an enum field
	// takes an enum name). It hides what a client must ask for explicitly,
	// e.g. {"deleted": false}: soft-deleted items appear only when the filter
	// tests deleted (AIP-164 without a show_deleted field).
	defaultEqualities *coremaps.ImmutableMap[string, any] `optgen:"default=equalityMap(nil)" optfn:"DefaultEqualities=equalityMap"`
	// maxFilterDepth bounds the nesting of a filter expression.
	maxFilterDepth int `optgen:"default=DefaultMaxFilterDepth" optval:"positive"`
	// maxFilterOperations bounds the AST nodes a filter may visit.
	maxFilterOperations int `optgen:"default=DefaultMaxFilterOperations" optval:"positive"`
	// defaultPageSize is the page size of a request with pageSize 0; it must
	// not exceed maxPageSize.
	defaultPageSize int `optgen:"default=DefaultPageSize" optval:"positive"`
	// maxPageSize clamps a larger requested page size. An engine may serve
	// a lower ceiling and then refuses the schema (the MongoDB engine reads
	// at most mongo.MaxListPageSize).
	maxPageSize int `optgen:"default=DefaultMaxPageSize" optval:"positive"`
	// unreadableSortAllowed accepts a sort pattern over a Hidden field and an
	// order a request's ReadMask does not cover. A page token then discloses
	// the sort values of the last item of a page to the client; set it only
	// when that is intended or the tokens are encrypted (pagetoken.WithEncryption).
	unreadableSortAllowed bool `optval:"param"`
}

func functionMap(values map[string]filter.CustomFunction) *coremaps.ImmutableMap[string, filter.CustomFunction] {
	return coremaps.NewImmutableMap(values)
}

func equalityMap(values map[string]any) *coremaps.ImmutableMap[string, any] {
	return coremaps.NewImmutableMap(values)
}

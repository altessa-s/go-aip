// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"github.com/altessa-s/go-aip/fieldpolicy"
)

//go:generate optgen generate --type=translatorOptions --output=translator_options_gen.go --option-type=TranslatorOption

// translatorOptions is the optgen target holding how one backend expresses a
// projection validated by a [Policy]: the API-to-storage mapping, storage
// paths that are never returned and storage paths that are always returned,
// all in storage names. Cross-package access goes through the public
// [TranslatorContext] alias and its read methods. Derived fields are resolved
// once by [NewTranslatorContext].
type translatorOptions struct {
	//optgen:doc FieldMapping WithFieldMapping sets an exact mapping from API names to storage names.
	//optgen:doc FieldPrefixMapping WithFieldPrefixMapping rewrites the leading segments of dotted API paths
	//optgen:doc FieldPrefixMapping ("address." → "addr."). A missing trailing dot is added to keys and values. The longest prefix wins
	//optgen:doc FieldPrefixMapping and exact [WithFieldMapping] entries win over any prefix.
	fieldMapping fieldpolicy.Mapping `optfn:"FieldMapping=.WithExact, FieldPrefixMapping=.WithPrefixes"`
	// WithDeniedStorageFields lists storage paths that are never returned,
	// whatever API name maps onto them — for example the paths
	// mapping/behavior/translators/mongo derives from `behavior:"input_only"`
	// tags. Matching follows the same ancestor/descendant rule as
	// [WithDeniedFields].
	deniedStorageFields []string `opt:"DeniedStorageFields" optgen:"allownull" optval:"clone,notrim,nodup"`
	// WithRequiredFields lists storage paths that every inclusion projection
	// carries regardless of the request: the document ID, a tenant key, a
	// version used for optimistic locking, or the sort keys a keyset paginator
	// reads from the last row. They are what the backend needs, not what the
	// client asks for, so they belong to the translator. A required path that
	// overlaps a denied one is a construction error.
	requiredFields []string `opt:"RequiredFields" optgen:"allownull" optval:"clone,notrim,nodup"`
	// WithStoragePathValidator replaces the dotted path grammar the denied
	// and required storage paths are checked against. Storage names follow
	// the backend, not the API: the policy's [WithPathValidator] — for
	// example a proto descriptor validator — never applies to them. A nil
	// validator keeps the default grammar.
	storagePathValidator PathValidator `opt:"StoragePathValidator" optgen:"default=checkPath"`

	// policy is the client-facing policy bound by NewTranslatorContext.
	policy *Policy `opt:"-"`
	// deniedStorage is deniedStorageFields plus every storage path a denied
	// API field of the policy reaches through the mapping — its own name, the
	// targets its descendants are moved to, and the names an exact ancestor
	// entry implies — so an allowed alias cannot reach a protected column.
	// Derived in NewTranslatorContext.
	deniedStorage []string `opt:"-"`
	// viewSelections holds the resolved projection of every view the policy
	// serves; ViewFull is every authorized field, the projection of an
	// omitted read mask and of "*". Derived in NewTranslatorContext.
	viewSelections map[View]Selection `opt:"-"`
}

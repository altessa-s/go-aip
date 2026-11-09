// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

import (
	"github.com/altessa-s/go-aip/fieldpolicy"
)

//go:generate optgen generate --type=translatorOptions --output=translator_options_gen.go --option-type=TranslatorOption

// translatorOptions is the optgen target holding how one backend expresses an order validated by a [Policy]: the API-to-storage
// mapping, custom key resolution, storage-level deny-list and server tie-breakers, all in storage names. The struct is
// unexported so callers cannot bypass [NewTranslatorContext]; cross-package access goes through the [TranslatorContext] alias.
type translatorOptions struct {
	// WithDeniedStorageFields lists storage paths no key may resolve to, whatever API name maps onto them.
	deniedStorageFields []string `opt:"DeniedStorageFields" optgen:"allownull" optval:"clone,notrim,nodup"`
	// WithTiebreaker appends trusted storage keys after mapping client keys; existing storage keys retain their direction.
	tiebreaker []Key `opt:"Tiebreaker" optgen:"allownull" optval:"clone"`
	// WithKeyResolver replaces the field mapping with custom resolution of an authorized API key to a storage key.
	keyResolver KeyResolver `opt:"KeyResolver" optgen:"notnil"`

	//optgen:doc FieldMapping WithFieldMapping sets an exact mapping from API field names to storage names.
	//optgen:doc FieldMapping This lets callers expose user-friendly names in the API surface while sorting on the underlying storage
	//optgen:doc FieldMapping representation. The mapping is copied into an [coremaps.ImmutableMap] so subsequent
	//optgen:doc FieldMapping mutations of the caller's map cannot affect a constructed translator.
	//optgen:doc FieldMapping
	//optgen:doc FieldMapping Example:
	//optgen:doc FieldMapping
	//optgen:doc FieldMapping 	trans, err := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(orderby.WithFieldMapping(map[string]string{
	//optgen:doc FieldMapping 	    "createdAt": "created_at",
	//optgen:doc FieldMapping 	})))
	//optgen:doc FieldPrefixMapping WithFieldPrefixMapping sets a mapping that rewrites the leading
	//optgen:doc FieldPrefixMapping segments of a dotted field path. Keys and values are normalized to end in "." so rewrites stay on segment boundaries.
	//optgen:doc FieldPrefixMapping
	//optgen:doc FieldPrefixMapping When multiple prefixes match, the longest one wins. Exact
	//optgen:doc FieldPrefixMapping [WithFieldMapping] entries always win over any prefix rewrite.
	//optgen:doc FieldPrefixMapping
	//optgen:doc FieldPrefixMapping Example:
	//optgen:doc FieldPrefixMapping
	//optgen:doc FieldPrefixMapping 	trans, err := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(orderby.WithFieldPrefixMapping(map[string]string{
	//optgen:doc FieldPrefixMapping 	    "address.":      "addr.",        // address.city.zip → addr.city.zip
	//optgen:doc FieldPrefixMapping 	    "user.profile.": "users.prof.",  // longest-match wins over "user."
	//optgen:doc FieldPrefixMapping 	})))
	fieldMapping fieldpolicy.Mapping `optfn:"FieldMapping=.WithExact, FieldPrefixMapping=.WithPrefixes"`

	// policy is the client-facing policy bound by NewTranslatorContext.
	policy *Policy `opt:"-"`
	// deniedStorage is deniedStorageFields plus every storage path a denied API field of the policy reaches through the
	// mapping. Derived in NewTranslatorContext.
	deniedStorage []string `opt:"-"`
}

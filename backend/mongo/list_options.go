// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

//go:generate optgen generate --type=listOptions --option-type=ListEngineOption --option-prefix=List --output=list_options_gen.go

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/listing"
)

// DefaultMaxTime caps one list query; a filter on unindexed fields scans, and the cap turns a runaway scan into a clear error.
const DefaultMaxTime = 10 * time.Second

// listOptions holds the optional settings of a [ListEngine]. The collection, the page-token signer and the schema are
// positional arguments of [NewListEngine].
type listOptions struct {
	// timestampPrecision requires cursor timestamps to align to this resolution, a positive multiple of a BSON millisecond.
	timestampPrecision time.Duration `optgen:"default=listing.TimestampPrecision"`
	// tiebreakPath names a unique BSON field, appended to every sort in the direction of its last key.
	tiebreakPath string `optgen:"default=DefaultTiebreakPath"`
	// parentKey explicitly binds a declared scope key to Request.Parent. Empty disables parent binding.
	parentKey string `optfn:"ParentKey=parentKeyValue"`
	// errorMapper overrides query failure classification. It receives the original error.
	errorMapper listing.ErrorMapper
	// fieldPaths maps a public field name to its BSON path, filtered and sorted on; a field without an entry is stored under its
	// public name. The id field maps to "_id" (the tie-breaker).
	fieldPaths map[string]string
	// sortPaths maps a public field name to the BSON path orderBy sorts on while the filter keeps its field path: it separates a
	// public filter field from an internal normalized sort field, e.g. cost vs costSort.
	sortPaths map[string]string
	// scopeKeys are BSON paths every query of the list pins by equality in its scope (the parent id of a child list). They
	// prefix every pattern index, ascending, so a scoped page is still an index range without a blocking sort.
	scopeKeys []string `optgen:"strings,unique,trim"`
	// indexNames names the index backing a sort pattern, keyed by the pattern or its reversal in any spelling. A pattern
	// without an entry gets the name MongoDB would generate from its keys ("owner_1_created_at_-1__id_-1"); an unscoped id-only
	// pattern is served by the built-in "_id_" index and takes no name.
	indexNames map[string]string
	// maxTime caps one list query (find); exceeding it is ErrTimeout. Zero relies on the caller's context deadline only.
	maxTime time.Duration `optgen:"default=DefaultMaxTime" optval:"nonpositive"`
	// projection limits the returned fields; it must keep every sort field and the tie-breaker.
	projection bson.D
	// registry encodes the filter the page token binds and the server receives, and the scope pins; pass the registry of the
	// collection's client when it is not NewRegistry. Nil uses NewRegistry.
	registry *bson.Registry
	// corruptRecord classifies a stored document that does not decode into the page type; the decode error is its hidden cause.
	// A nil value keeps the default.
	corruptRecord error `optgen:"default=ErrCorruptRecord,notnil"`
}

func parentKeyValue(v string) string { return strings.TrimSpace(v) }

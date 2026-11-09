// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"time"

	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

//go:generate optgen generate --type=listOptions --option-type=ListEngineOption --option-prefix=List --output=list_options_gen.go

// DefaultTimestampPrecision is the listing cursor resolution.
const DefaultTimestampPrecision = listing.TimestampPrecision

// DefaultMaxWork is the default request work budget of a list: one unit per scanned item, field step, predicate visit, unit of
// compared data and sort step. It serves the small collections the list engine is for, bounding what one request can cost on a
// collection that grew past them. WithListMaxWork(0) lifts the limit.
const DefaultMaxWork = 1 << 24

type listOptions struct {
	// tiebreakField overrides the identity callback with a public field path.
	tiebreakField string
	// timestampPrecision is the cursor timestamp resolution; storage must use this resolution.
	timestampPrecision time.Duration `optgen:"default=DefaultTimestampPrecision"`
	// positionCodec controls position normalization, comparison and encoding.
	positionCodec pagetoken.PositionCodec `optgen:"default=pagetoken.ScalarPositionCodec{},notnil"`

	// maxDataBudget bounds each field read and predicate evaluation.
	maxDataBudget int `optgen:"default=DefaultMaxDataBudget" optval:"positive"`
	// profile replaces portable evaluation; it must agree with the schema contract.
	profile evaluator.Profile
	// maxWork bounds scans, field reads, predicate visits, and sorting per request (DefaultMaxWork); zero is unlimited.
	maxWork int `optgen:"default=DefaultMaxWork"`
	// maxScanned bounds input items per request; zero is unlimited.
	maxScanned int
	// maxMaterialized bounds the rows held in the page heap (at most the page size plus one); zero is unlimited.
	maxMaterialized int
	valueEncoding   evaluator.ValueEncoding
}

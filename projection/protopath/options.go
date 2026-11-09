// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package protopath

//go:generate optgen generate --type=options --output=options_gen.go --option-type=Option

// DefaultMaxDepth is the default maximum number of dot-separated segments in
// a validated path. It matches the default MaxFieldPathDepth of the
// projection parser.
const DefaultMaxDepth = 8

// options holds [Validator] configuration. All fields are populated by the
// generated [Option] setters in options_gen.go.
type options struct {
	// WithJSONNames also accepts the json_name of each field (e.g. "displayName" for display_name). Proto field names are always
	// accepted.
	jsonNames bool `opt:"JSONNames" optval:"param"`
	// WithIgnoreUnknown accepts a path that names a field the descriptor does not declare instead of reporting [ErrUnknownField]
	// (AIP-161 lets read masks ignore such paths). The path must still be well-formed, and the declared prefix before the unknown
	// segment must still follow the traversal rules.
	ignoreUnknown bool `opt:"IgnoreUnknown" optval:"param"`
	// WithMaxDepth bounds the number of dot-separated segments of a path. A longer path fails with
	// projection.ErrMaxFieldPathDepthExceeded.
	maxDepth int `opt:"MaxDepth" optgen:"default=DefaultMaxDepth" optval:"positive"`
}

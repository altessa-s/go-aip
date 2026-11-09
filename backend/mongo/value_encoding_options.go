// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import "go.mongodb.org/mongo-driver/v2/bson"

//go:generate optgen generate --type=valueEncodingOptions --option-type=ValueEncodingOption --option-prefix=ValueEncoding --output=value_encoding_options_gen.go

// DefaultMaxDocumentBytes bounds the BSON document a custom registry ([WithValueEncodingRegistry]) may encode one value to: MongoDB's
// document limit.
const DefaultMaxDocumentBytes = 16 << 20

type valueEncodingOptions struct {
	// registry encodes values with the application's BSON codecs instead of [NewRegistry]. Register every codec before
	// constructing the encoding; the registry must not change afterwards.
	registry *bson.Registry
	// maxDocumentBytes bounds the BSON a custom registry may encode one value to; a larger encoding leaves the value opaque.
	maxDocumentBytes int `optgen:"default=DefaultMaxDocumentBytes" optval:"positive"`
}

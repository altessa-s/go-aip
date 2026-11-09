// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag

//go:generate optgen generate --type=options --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=hashOptions --option-type=HashOption --output=hash_options_gen.go

import (
	"google.golang.org/genproto/googleapis/api/annotations"
)

// FieldName is the AIP-154 name of the etag field of a resource and of a
// Delete or custom method request.
const FieldName = "etag"

// DefaultMaxLength is the default value of [WithMaxLength]: the longest etag
// a client may send, in bytes. Issued tags are far shorter (a content hash
// is 43 bytes, a revision at most 20).
const DefaultMaxLength = 256

// Comparison selects how a [Precondition] matches the etag a client sends
// against the current one.
type Comparison int

// Comparisons of a [Precondition]. An unknown value compares like
// [CompareExact].
const (
	// CompareExact, the default, requires the sent tag to be the current
	// tag as issued: the same value and the same weakness. A client echoes
	// the etag it read, so a weak etag the service issues satisfies the
	// precondition while a strong rewrite of it does not.
	CompareExact Comparison = iota
	// CompareStrong is the RFC 9110 §8.8.3.2 strong comparison of If-Match:
	// both tags must be strong and equal, so a service issuing weak tags
	// can never satisfy it.
	CompareStrong
	// CompareWeak is the RFC 9110 §8.8.3.2 weak comparison: the values must
	// be equal, whatever their weakness.
	CompareWeak
)

// options configures [Parse] and a [Precondition].
type options struct {
	// field is the API path of the etag field a [Precondition] names in
	// its errors, for example "book.etag" for an Update request or "etag"
	// for a Delete request (AIP-193).
	field string `optgen:"default=FieldName"`
	// required makes a [Precondition] refuse a request without an etag
	// ([ReasonRequired]) instead of applying it unconditionally (AIP-154
	// allows services with strong consistency requirements to require it).
	required bool `optval:"param"`
	// comparison is how a [Precondition] matches the sent tag against the
	// current one.
	comparison Comparison
	// maxLength bounds the bytes of a sent etag; longer values are refused
	// before any parsing. Zero is unlimited; a negative value keeps the
	// default.
	maxLength int `optgen:"default=DefaultMaxLength" optval:"positive=allow_zero"`
	// strictQuotes refuses a bare (unquoted) etag: only `"value"` and
	// `W/"value"` are accepted, as RFC 9110 and AIP-154 recommend.
	strictQuotes bool `optval:"param"`
	// singleton lets [Precondition.ParseRevision] accept W/"0", the etag
	// of a singleton resource that has not been saved yet; every other
	// stored revision is positive.
	singleton bool `optval:"param"`
}

// hashOptions configures [FromMessage].
type hashOptions struct {
	// hashFields limits the hash to these field paths: top-level field
	// names or dotted paths through singular message fields, by proto
	// name. Empty hashes every field.
	hashFields []string `opt:"HashFields" optgen:"allownull"`
	// excludeFields removes these field paths, spelled like
	// [WithHashFields], from the hash. The top-level etag field is always
	// excluded.
	excludeFields []string `opt:"ExcludeFields" optgen:"allownull"`
	// excludeBehaviors removes the fields carrying these
	// google.api.field_behavior values from the hash at any depth, for
	// example OUTPUT_ONLY timestamps a store rewrites on every save.
	excludeBehaviors []annotations.FieldBehavior `opt:"ExcludeBehaviors" optgen:"allownull"`
	// weak makes the tag weak (W/"..."): use it when exclusions make equal
	// tags mean semantically, not byte-for-byte, equal resources.
	weak bool `optval:"param"`
	// hex encodes the digest in lowercase hex (64 bytes) instead of
	// unpadded base64url (43 bytes).
	hex bool `opt:"HexEncoding" optval:"param"`
}

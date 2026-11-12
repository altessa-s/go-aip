// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior

import (
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
)

// Violation describes a single field-behavior constraint that was violated
// during update-mask application or payload sanitization. It is the shared
// shape used by both [github.com/altessa-s/go-aip/fieldmask/update]
// and [github.com/altessa-s/go-aip/fieldbehavior] so the gRPC
// interceptor can render either source through the same
// io.altessa.badrequest.v1.BadRequest FieldViolation mapping.
//
// Reason is populated by callers that classify the violation with a
// human-readable explanation (fieldmask/update's required/immutable/identifier
// reasons). Behavior is populated by callers that surface the raw
// google.api.field_behavior annotation (fieldbehavior's strict-mode strip).
// Format is populated only for a value that breaks the AIP-202 format of its
// field (fieldbehavior's format checks), whose Behavior is then
// FIELD_BEHAVIOR_UNSPECIFIED. These fields are optional — only Path is
// guaranteed to be set.
type Violation struct {
	// Path is the dot-separated path to the offending field. Repeated and
	// map entries may be indexed by the producer ("aliases[2].id",
	// `labels["foo"].id`).
	Path string

	// Behavior is the google.api.field_behavior value that triggered the
	// violation. Zero (FIELD_BEHAVIOR_UNSPECIFIED) when the producer did not
	// classify the violation by annotation.
	Behavior annotations.FieldBehavior

	// Reason is a human-readable explanation of why the field was rejected.
	// Empty when the producer relies solely on Behavior to convey intent.
	Reason string

	// Format is the name of the format a value breaks: an AIP-202
	// google.api.FieldInfo.Format enum name ("UUID4", "IPV4", "IPV6",
	// "IPV4_OR_IPV6") or a custom format name ("email"). Empty when the
	// violation is not a format one.
	Format string
}

// JoinPath returns prefix joined with name using a dot separator. An empty
// prefix yields name unchanged so callers can build paths incrementally
// without special-casing the root.
func JoinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}

	return prefix + "." + name
}

// QuoteSegment writes one segment of a dot-separated AIP-161 field path,
// such as a map key: as is when it is a plain segment, and between
// backticks, with every backtick in it doubled, when it is empty or holds a
// dot or a backtick. Without quoting such a segment would be read back as
// several segments or as a malformed path.
// It is the quoting of the fieldmask path grammar, so a violation path names
// a field the way a mask path does.
func QuoteSegment(seg string) string {
	if seg != "" && !strings.ContainsAny(seg, ".`") {
		return seg
	}

	return "`" + strings.ReplaceAll(seg, "`", "``") + "`"
}

// Reasons of an IMMUTABLE or IDENTIFIER field an update cannot accept
// (AIP-203: an unchanged value is ignored, a change is INVALID_ARGUMENT).
const (
	// ReasonImmutableChanged reports an IMMUTABLE value that differs from the
	// stored one.
	ReasonImmutableChanged = "immutable field cannot be changed from its stored value"
	// ReasonIdentifierChanged reports an IDENTIFIER value that differs from
	// the stored one.
	ReasonIdentifierChanged = "identifier field cannot be changed from its stored value"
)

// ProtectedViolation returns the violation of a protected field whose value
// differs from the stored resource.
func ProtectedViolation(path string, b annotations.FieldBehavior) Violation {
	reason := ReasonImmutableChanged
	if b == annotations.FieldBehavior_IDENTIFIER {
		reason = ReasonIdentifierChanged
	}

	return Violation{Path: path, Behavior: b, Reason: reason}
}

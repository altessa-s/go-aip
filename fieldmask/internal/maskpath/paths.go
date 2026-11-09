// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskpath

import (
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// Separator is the delimiter of path segments.
	Separator = "."

	// Wildcard is the AIP-161 path segment matching every element of a
	// repeated field or every value of a map field. Inside a mask tree it is
	// stored as a regular key: a protobuf field name cannot hold "*".
	Wildcard = "*"

	// ReasonWildcardNotAlone is the reason of "*" combined with other paths.
	ReasonWildcardNotAlone = `"*" must be the only path`

	// ReasonMalformedPath is the reason, formatted with the path, of a path
	// the grammar rejects, or of a "*" that does not follow a repeated or map
	// field.
	ReasonMalformedPath = "malformed path %q"
)

// Error is a path the grammar or a schema rejects: Path as written and the
// Reason. The public packages report it as *fieldmask.ValidationError.
type Error struct {
	Path   string
	Reason string
}

// Error returns the error message.
func (e *Error) Error() string {
	return "invalid field mask path '" + e.Path + "': " + e.Reason
}

// Split returns the unescaped segments of path by the path grammar (AIP-161):
// dot-separated, a backtick-quoted segment such as “reviews.`John Smith`“ or
// “metadata.`google.com/project`“ kept whole with its backticks removed and
// every doubled backtick inside it made single. A malformed path comes back
// verbatim as its only segment, as fieldmask.FromPaths stores it; an empty
// path has no segments.
func Split(path string) []string {
	if path == "" {
		return nil
	}

	segs, err := Parse(path)
	if err != nil {
		return []string{path}
	}

	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.Text
	}

	return parts
}

// RejectStructural returns an *[Error] when path addresses a single element
// of a repeated field or a ListValue ("authors.0", "authors.0.given_name",
// "list_data.0"), which AIP-161 forbids in every mask, or, with strict, a
// sub-path into the dynamic value of a google.protobuf.Struct entry, a Value
// or a ListValue ("extra_data.color.hue"). Every other path problem is
// ignored: this check runs even when schema validation is turned off, and
// [ResolvePath] reports the rest.
func RejectStructural(descriptor protoreflect.MessageDescriptor, path string, strict bool) error {
	_, err := Check{Aliases: true, StructuralOnly: true, Strict: strict}.Resolve(descriptor, path)
	return err
}

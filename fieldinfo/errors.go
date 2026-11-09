// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo

import (
	"errors"
	"fmt"
	"strings"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Sentinel errors.
var (
	// ErrInvalidValue classifies a value that does not match its format: a
	// *[ValueError] from the value functions, and every *[ViolationError].
	ErrInvalidValue = errors.New("fieldinfo: value does not match its format")
	// ErrUnknownFormat is returned by the value functions for a format this
	// package does not know (a value added to google.api.FieldInfo.Format
	// after this release). The message walks skip such fields instead.
	ErrUnknownFormat = errors.New("fieldinfo: unknown format")
	// ErrMaxDepthExceeded is returned when a walk descends more nested
	// messages than [WithMaxDepth] permits.
	ErrMaxDepthExceeded = errors.New("fieldinfo: max traversal depth exceeded")
	// ErrInvalidRegistry is returned by [Registry.Register] and
	// [Registry.Assign] for an empty name, a nil validator, a name registered
	// twice, a field assigned twice or a field assigned an unregistered name.
	ErrInvalidRegistry = errors.New("fieldinfo: invalid custom format registration")
)

// ValueError reports a value that does not match its format. It matches
// [ErrInvalidValue] and [coreerrs.ErrInvalidArgument]; it never holds the
// value, which may be personal data (an IP address).
type ValueError struct {
	// Format is the name of the format: an AIP-202 enum name ("UUID4") or
	// the name of a custom format.
	Format string
	// Reason says what a valid value looks like.
	Reason string
}

// Error renders "fieldinfo: <format>: <reason>".
func (e *ValueError) Error() string {
	return "fieldinfo: " + e.Format + ": " + e.Reason
}

// Unwrap returns [ErrInvalidValue] and [coreerrs.ErrInvalidArgument].
func (e *ValueError) Unwrap() []error {
	return []error{ErrInvalidValue, coreerrs.ErrInvalidArgument}
}

// Violation is one field value that does not match its format.
type Violation struct {
	// Path is the dot-separated path of the value: a repeated element is
	// indexed ("dnsServers.1" or "dns_servers.1") and a map value keyed
	// ("peers.edge"), with a key that is empty or holds a dot or a backtick
	// backtick-quoted, as in fieldmask and fieldbehavior paths.
	Path string
	// Format is the name of the format the value breaks: an AIP-202 enum
	// name or a custom format name.
	Format string
	// Reason says what a valid value looks like.
	Reason string
}

// ViolationError lists every value of a message that does not match its
// format, in walk order (declaration order, repeated elements by index, map
// values by key). It matches [ErrInvalidValue] and
// [coreerrs.ErrInvalidArgument] (INVALID_ARGUMENT).
type ViolationError struct {
	Violations []Violation
}

// Error lists the violations.
func (e *ViolationError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "fieldinfo: %d violation(s)", len(e.Violations))

	for i, v := range e.Violations {
		sep := "; "
		if i == 0 {
			sep = ": "
		}

		fmt.Fprintf(&sb, "%s%s (%s) %s", sep, v.Path, v.Format, v.Reason)
	}

	return sb.String()
}

// Unwrap returns [ErrInvalidValue] and [coreerrs.ErrInvalidArgument].
func (e *ViolationError) Unwrap() []error {
	return []error{ErrInvalidValue, coreerrs.ErrInvalidArgument}
}

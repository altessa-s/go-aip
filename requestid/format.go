// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import (
	"fmt"
	"strings"
)

// MaxLength is the length limit of a request ID: AIP-155 restricts it to 36
// ASCII characters, the length of a UUID in its canonical text form.
const MaxLength = 36

// Format is the accepted format of a request ID. AIP-155 says request IDs
// should be able to be UUIDs and may allow UUIDs to be the only valid format;
// the format a service accepts must be documented.
type Format int

const (
	// FormatUUID accepts a UUID of any version and variant in the canonical
	// 8-4-4-4-12 hexadecimal text form of RFC 9562, in either letter case.
	// It is the default: AIP-202 normalizes a UUID4 value to lowercase and
	// itself shows a value whose version nibble is not 4, so the version is
	// not enforced. The Nil and Max UUIDs are rejected: they are not unique.
	FormatUUID Format = iota
	// FormatUUID4 is [FormatUUID] restricted to version 4 (random) UUIDs of
	// the RFC 9562 variant: the version nibble must be 4 and the variant
	// bits 10.
	FormatUUID4
	// FormatASCII accepts any 1 to 36 printable ASCII characters (0x21 to
	// 0x7E), compared exactly: the "restricted to 36 ASCII characters"
	// wording of AIP-155 for services whose request IDs are not UUIDs.
	FormatASCII
)

// String returns the name of f.
func (f Format) String() string {
	switch f {
	case FormatUUID:
		return "UUID"
	case FormatUUID4:
		return "UUID4"
	case FormatASCII:
		return "ASCII"
	default:
		return fmt.Sprintf("Format(%d)", int(f))
	}
}

func (f Format) valid() bool { return f >= FormatUUID && f <= FormatASCII }

// Canonical validates raw as a request ID of format f and returns its
// canonical form, the value deduplication compares: a UUID in lowercase, an
// ASCII ID unchanged. An empty raw is no request ID and returns "" without
// error. An invalid raw returns an error matching [ErrInvalidRequestID]
// whose text says why, without echoing the value.
func Canonical(raw string, f Format) (string, error) {
	if raw == "" {
		return "", nil
	}

	if len(raw) > MaxLength {
		return "", invalidf("the request ID is %d bytes long, more than the limit of %d", len(raw), MaxLength)
	}

	switch f {
	case FormatUUID, FormatUUID4:
		return canonicalUUID(raw, f == FormatUUID4)
	case FormatASCII:
		for i := range len(raw) {
			if raw[i] < '!' || raw[i] > '~' {
				return "", invalidf("the request ID must hold printable ASCII characters only")
			}
		}

		return raw, nil
	default:
		return "", fmt.Errorf("requestid: unknown %v", f)
	}
}

// canonicalUUID checks the canonical 8-4-4-4-12 text form and lowercases it.
func canonicalUUID(raw string, v4 bool) (string, error) {
	const reason = "the request ID must be a UUID in the 8-4-4-4-12 hexadecimal form"

	if len(raw) != MaxLength {
		return "", invalidf(reason)
	}

	allZero, allF := true, true

	for i := range len(raw) {
		c := raw[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", invalidf(reason)
			}

			continue
		}

		c = lowerHex(c)
		if c == 0 {
			return "", invalidf(reason)
		}

		allZero = allZero && c == '0'
		allF = allF && c == 'f'
	}

	if allZero || allF {
		return "", invalidf("the request ID must not be the Nil or Max UUID")
	}

	if v4 {
		// The version nibble is the first digit of the third group, the
		// variant the first digit of the fourth: 8, 9, a or b is 10xx.
		if raw[14] != '4' || !strings.ContainsRune("89abAB", rune(raw[19])) {
			return "", invalidf("the request ID must be a version 4 UUID")
		}
	}

	return strings.ToLower(raw), nil
}

// lowerHex returns the lowercase form of the hexadecimal digit c, or 0.
func lowerHex(c byte) byte {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		return c
	case c >= 'A' && c <= 'F':
		return c + ('a' - 'A')
	default:
		return 0
	}
}

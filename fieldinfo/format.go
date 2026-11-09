// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo

import (
	"net/netip"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
)

// Format aliases the google.api.FieldInfo.Format enum of AIP-202.
type Format = annotations.FieldInfo_Format

// The AIP-202 formats.
const (
	// Unspecified is the default: the field has no standard format.
	Unspecified = annotations.FieldInfo_FORMAT_UNSPECIFIED
	// UUID4 is an RFC 9562 (formerly RFC 4122) version 4 UUID.
	UUID4 = annotations.FieldInfo_UUID4
	// IPv4 is an RFC 791 IPv4 address in dotted-decimal form.
	IPv4 = annotations.FieldInfo_IPV4
	// IPv6 is an RFC 4291 IPv6 address.
	IPv6 = annotations.FieldInfo_IPV6
	// IPv4OrIPv6 is an address of either family.
	IPv4OrIPv6 = annotations.FieldInfo_IPV4_OR_IPV6
)

// Reasons of a value that does not match its format. They are the Reason of
// a [Violation] and the text of a [*ValueError].
const (
	ReasonUUID4      = "must be a version 4 UUID in the 8-4-4-4-12 hexadecimal form"
	ReasonIPv4       = "must be an IPv4 address in dotted-decimal form"
	ReasonIPv6       = "must be an IPv6 address without a zone"
	ReasonIPv4OrIPv6 = "must be an IPv4 or IPv6 address"
)

// Maximum lengths of a valid value: longer input is rejected before it is
// parsed.
const (
	uuidLen    = len("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	maxIPv4Len = len("255.255.255.255")
	// Eight four-digit groups, or six groups and an embedded IPv4 address.
	maxIPv6Len = len("ffff:ffff:ffff:ffff:ffff:ffff:255.255.255.255")
)

// Positions in the 8-4-4-4-12 UUID form.
const (
	uuidHyphen1   = 8
	uuidHyphen2   = 13
	uuidVersion   = 14
	uuidHyphen3   = 18
	uuidVariant   = 19
	uuidHyphen4   = 23
	maxOctet      = 255
	maxOctetLen   = len("255")
	decimalRadix  = 10
	ipv4OctetsLen = 4
)

// Known reports whether f is a format this package validates.
func Known(f Format) bool {
	switch f {
	case UUID4, IPv4, IPv6, IPv4OrIPv6:
		return true
	case Unspecified:
		return false
	default:
		return false
	}
}

// ValidateValue reports whether s is a valid value of format f (AIP-202):
//   - UUID4: 32 hexadecimal digits, of either case, in the 8-4-4-4-12 form,
//     with version 4 and the RFC 9562 variant. Braces, a "urn:uuid:" prefix
//     and the unhyphenated form are rejected.
//   - IPv4: four dot-separated decimal octets of one to three digits, each at
//     most 255. Leading zeros are accepted and read as decimal ("001.022.233.040"
//     is 1.22.233.40), as AIP-202 allows a service to condense them.
//   - IPv6: any RFC 4291 text form, of either case, with "::" compression and
//     an embedded dotted-decimal IPv4 tail. A zone ("fe80::1%eth0") or
//     brackets are rejected: they are not part of the address.
//   - IPv4OrIPv6: an IPv6 value when s holds a colon, an IPv4 value otherwise.
//
// It returns nil for a valid value and for [Unspecified], a *[ValueError]
// (matching [ErrInvalidValue]) for an invalid one, and [ErrUnknownFormat] for
// a format this package does not know. The error never holds the value.
func ValidateValue(f Format, s string) error {
	if f != Unspecified && !Known(f) {
		return ErrUnknownFormat
	}

	if ve := validateKnown(f, s); ve != nil {
		return ve
	}

	return nil
}

// validateKnown validates s against the known format f without allocating
// for a valid value; any other format accepts every value.
func validateKnown(f Format, s string) *ValueError {
	var (
		ok     bool
		reason string
	)

	switch f {
	case UUID4:
		ok, reason = isUUID4(s), ReasonUUID4
	case IPv4:
		_, ok = parseIPv4(s)
		reason = ReasonIPv4
	case IPv6:
		_, ok = parseIPv6(s)
		reason = ReasonIPv6
	case IPv4OrIPv6:
		_, ok = parseIP(s)
		reason = ReasonIPv4OrIPv6
	default:
		return nil
	}

	if ok {
		return nil
	}

	return &ValueError{Format: f.String(), Reason: reason}
}

// NormalizeValue validates s as [ValidateValue] does and returns its
// normalized form (AIP-202): a UUID in lowercase, an IPv4 address with the
// leading zeros of each octet stripped, and an IPv6 address in the RFC 5952
// canonical form (lowercase, leading zeros of each group stripped, the
// longest run of zero groups compressed to "::"; an IPv4-mapped address keeps
// its dotted tail, "::ffff:192.0.2.1"). [Unspecified] returns s unchanged.
func NormalizeValue(f Format, s string) (string, error) {
	switch f {
	case Unspecified:
		return s, nil
	case UUID4:
		return normalizeUUID4(s)
	case IPv4:
		return normalizeIPv4(s)
	case IPv6:
		return normalizeIPv6(s)
	case IPv4OrIPv6:
		a, ok := parseIP(s)
		if !ok {
			return "", &ValueError{Format: f.String(), Reason: ReasonIPv4OrIPv6}
		}

		return a.String(), nil
	default:
		return "", ErrUnknownFormat
	}
}

// Equal reports whether a and b denote the same value of format f. AIP-202
// forbids comparing such values as plain text: Equal compares their
// normalized forms, so "F47AC10B-58CC-4372-A567-0E02B2C3D479" equals its
// lowercase spelling and "2001:0DB8:0::0" equals "2001:db8::". An IPv4
// address and its IPv4-mapped IPv6 form are different values. It returns the
// error of [NormalizeValue] when either value is invalid.
func Equal(f Format, a, b string) (bool, error) {
	na, err := NormalizeValue(f, a)
	if err != nil {
		return false, err
	}

	nb, err := NormalizeValue(f, b)
	if err != nil {
		return false, err
	}

	return na == nb, nil
}

func normalizeUUID4(s string) (string, error) {
	if !isUUID4(s) {
		return "", &ValueError{Format: UUID4.String(), Reason: ReasonUUID4}
	}

	return strings.ToLower(s), nil
}

// isUUID4 reports whether s is a hyphenated version 4, RFC 9562 variant UUID.
func isUUID4(s string) bool {
	if len(s) != uuidLen {
		return false
	}

	for i := range len(s) {
		c := s[i]

		switch i {
		case uuidHyphen1, uuidHyphen2, uuidHyphen3, uuidHyphen4:
			if c != '-' {
				return false
			}
		case uuidVersion:
			if c != '4' {
				return false
			}
		case uuidVariant: // the two high bits are 10
			if !strings.ContainsRune("89abAB", rune(c)) {
				return false
			}
		default:
			if !isHex(c) {
				return false
			}
		}
	}

	return true
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func normalizeIPv4(s string) (string, error) {
	a, ok := parseIPv4(s)
	if !ok {
		return "", &ValueError{Format: IPv4.String(), Reason: ReasonIPv4}
	}

	return a.String(), nil
}

// parseIPv4 parses four dot-separated decimal octets of one to three digits,
// leading zeros included.
func parseIPv4(s string) (netip.Addr, bool) {
	if len(s) > maxIPv4Len {
		return netip.Addr{}, false
	}

	var octets [ipv4OctetsLen]byte

	n := 0
	for part := range strings.SplitSeq(s, ".") {
		if n == len(octets) || part == "" || len(part) > maxOctetLen {
			return netip.Addr{}, false
		}

		v := 0

		for i := range len(part) {
			c := part[i]
			if c < '0' || c > '9' {
				return netip.Addr{}, false
			}

			v = v*decimalRadix + int(c-'0')
		}

		if v > maxOctet {
			return netip.Addr{}, false
		}

		octets[n] = byte(v)
		n++
	}

	if n != len(octets) {
		return netip.Addr{}, false
	}

	return netip.AddrFrom4(octets), true
}

func normalizeIPv6(s string) (string, error) {
	a, ok := parseIPv6(s)
	if !ok {
		return "", &ValueError{Format: IPv6.String(), Reason: ReasonIPv6}
	}

	return a.String(), nil
}

// parseIP parses an IPv6 address when s holds a colon, an IPv4 address
// otherwise.
func parseIP(s string) (netip.Addr, bool) {
	if strings.Contains(s, ":") {
		return parseIPv6(s)
	}

	return parseIPv4(s)
}

// parseIPv6 parses an RFC 4291 IPv6 address without a zone.
func parseIPv6(s string) (netip.Addr, bool) {
	if len(s) > maxIPv6Len || !strings.Contains(s, ":") || strings.Contains(s, "%") {
		return netip.Addr{}, false
	}

	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is6() || a.Zone() != "" {
		return netip.Addr{}, false
	}

	return a, true
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import (
	"errors"
	"unicode/utf8"
)

// Wildcard is the "-" segment of AIP-159 that reads across the collections
// of a parent. It is never a valid resource ID.
const Wildcard = "-"

// MaxStrictIDLength is the length limit of [StrictID]: an RFC-1034 label.
const MaxStrictIDLength = 63

// IDValidator checks one resource ID segment of a name (a value matched by a
// pattern variable) and returns a non-nil error describing what is wrong.
// The error text is reported to the client as the field violation reason.
// It is never called with [Wildcard], which the [Validator] handles itself.
type IDValidator func(id string) error

var (
	errIDEmpty    = errors.New("is empty")
	errIDSlash    = errors.New(`holds "/"`)
	errIDUTF8     = errors.New("is not valid UTF-8")
	errIDControl  = errors.New("holds a control character")
	errIDDots     = errors.New(`is "." or ".."`)
	errIDWildcard = errors.New(`is the "-" wildcard`)
	errIDStrict   = errors.New("must match [a-z]([a-z0-9-]{0,61}[a-z0-9])? (RFC-1034)")
)

// PermissiveID is the default [IDValidator]. It accepts any non-empty valid
// UTF-8 ID except one holding "/" or a control character (U+0000–U+001F,
// U+007F–U+009F), "." and "..", which path-based routers resolve, and the
// "-" wildcard. Upper-case letters, server-generated IDs such as UUIDs and
// Unicode are accepted; Unicode normalization (NFC, AIP-210) is the API's
// responsibility.
func PermissiveID(id string) error {
	switch id {
	case "":
		return errIDEmpty
	case ".", "..":
		return errIDDots
	case Wildcard:
		return errIDWildcard
	}

	for i := 0; i < len(id); {
		c := id[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '/':
				return errIDSlash
			case c < 0x20 || c == 0x7f:
				return errIDControl
			}

			i++

			continue
		}

		r, size := utf8.DecodeRuneInString(id[i:])
		if r == utf8.RuneError && size <= 1 {
			return errIDUTF8
		}

		if r >= 0x80 && r <= 0x9f {
			return errIDControl
		}

		i += size
	}

	return nil
}

// StrictID is the [IDValidator] of the AIP-122 recommendation for
// user-specified IDs: an RFC-1034 label of lower-case letters, digits and
// hyphens, starting with a letter, not ending with a hyphen, at most
// [MaxStrictIDLength] characters: [a-z]([a-z0-9-]{0,61}[a-z0-9])?.
func StrictID(id string) error {
	if id == "" {
		return errIDEmpty
	}

	if len(id) > MaxStrictIDLength || !isLower(id[0]) || id[len(id)-1] == '-' {
		return errIDStrict
	}

	for i := 1; i < len(id); i++ {
		if c := id[i]; !isLower(c) && !isDigit(c) && c != '-' {
			return errIDStrict
		}
	}

	return nil
}

// ValidateUID checks the uid field of AIP-148: a UUID4 in its canonical
// textual form (8-4-4-4-12 hexadecimal digits, either case), version 4 and
// the RFC 9562 variant. Errors match [ErrInvalidUID].
func ValidateUID(uid string) error {
	// uuidTemplate is a UUID4: x a hexadecimal digit, y the variant digit.
	const uuidTemplate = "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx"

	if len(uid) != len(uuidTemplate) {
		return ErrInvalidUID
	}

	for i := range len(uuidTemplate) {
		c := uid[i]

		switch t := uuidTemplate[i]; t {
		case 'x':
			if !isHex(c) {
				return ErrInvalidUID
			}
		case 'y':
			if c != '8' && c != '9' && c != 'a' && c != 'b' && c != 'A' && c != 'B' {
				return ErrInvalidUID
			}
		default:
			if c != t {
				return ErrInvalidUID
			}
		}
	}

	return nil
}

func isHex(c byte) bool { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }

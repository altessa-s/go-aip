// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"strings"
	"unicode/utf8"
)

// escapeValue backslash-escapes every ASCII byte of s that is not a letter, a digit or the underscore: the RediSearch query syntax,
// its tokenizer's separators (punctuation and whitespace, a tab or a newline included), the fuzzy (%) and prefix (*) markers and the
// control characters. RediSearch reads an escaped character as part of the term, so a value is always one term of one field. The
// bytes of non-ASCII text pass unchanged.
func escapeValue(s string) string {
	n := 0

	for i := range len(s) {
		if needsEscape(s[i]) {
			n++
		}
	}

	if n == 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + n)

	for i := range len(s) {
		if needsEscape(s[i]) {
			b.WriteByte('\\')
		}

		b.WriteByte(s[i])
	}

	return b.String()
}

// needsEscape reports whether escapeValue escapes the byte c.
func needsEscape(c byte) bool {
	switch {
	case c >= utf8.RuneSelf, c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return false
	default:
		return true
	}
}

// EscapeTag is a public helper that escapes RediSearch special characters in TAG values: every ASCII character but a letter, a
// digit and the underscore, whitespace and control characters included. Useful for building raw RediSearch queries outside the
// translator.
func EscapeTag(s string) string {
	return escapeValue(s)
}

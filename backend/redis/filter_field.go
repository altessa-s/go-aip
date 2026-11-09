// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// validAttribute reports whether name is a storage name the translators may send as an attribute: non-empty valid UTF-8
// without white space or control characters, whose dot-separated segments are not empty. RediSearch cannot escape a control
// character in a query, and an empty segment or white space only reaches a field name through a client-chosen suffix the
// policy let through (a prefix allow-list such as "labels.*" or a prefix mapping), never through a real attribute.
func validAttribute(name string) bool {
	if name == "" || !utf8.ValidString(name) ||
		strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}

	for segment := range strings.SplitSeq(name, ".") {
		if segment == "" {
			return false
		}
	}

	return true
}

// queryField renders the attribute reference `@name` of a query: RediSearch unescapes a field name, so escaping every ASCII
// character but a letter, a digit and `_` keeps the whole name — dots, dashes and any syntax character included — one
// attribute.
func queryField(name string) string {
	return "@" + escapeValue(name)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import "strings"

// Every translator writes a storage attribute name into Meilisearch syntax as it is: the filter grammar reads an unquoted
// attribute as one word of letters, digits, `_`, `-` and `.`, so a name with any other character — white space, a quote, an
// operator, a parenthesis, a control character — would end the word early and turn the rest of the name into filter syntax. The
// translators therefore refuse such a name instead of emitting it, independently of the field rules of the policy, which may let
// a client-chosen suffix through (a prefix allow-list such as "labels.*" or a prefix mapping).
//
// An attribute name is one or more dot-separated segments, each made of ASCII letters, digits, `_` and `-`. The filter
// translator also refuses the words the filter grammar reserves.

// validAttribute reports whether name is an attribute name the translators may write unquoted: non-empty dot-separated
// segments of ASCII letters, digits, `_` and `-`.
func validAttribute(name string) bool {
	if name == "" {
		return false
	}

	for segment := range strings.SplitSeq(name, ".") {
		if segment == "" || strings.ContainsFunc(segment, func(r rune) bool { return !attributeRune(r) }) {
			return false
		}
	}

	return true
}

// attributeRune reports whether r may appear in an attribute segment.
func attributeRune(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_' || r == '-'
}

// reservedFilterWords are the words the Meilisearch filter grammar refuses as an unquoted value or attribute (its is_keyword,
// case-sensitive) and the geo and foreign-filter function names it parses before any attribute.
var reservedFilterWords = map[string]struct{}{
	"AND": {}, "OR": {}, "IN": {}, "NOT": {}, "TO": {}, "EXISTS": {}, "IS": {}, "NULL": {}, "EMPTY": {}, "CONTAINS": {},
	"STARTS": {}, "WITH": {}, "_geo": {}, "_geoRadius": {}, "_geoBoundingBox": {}, "_geoPolygon": {}, "_foreign": {},
}

// validFilterAttribute reports whether name may be written as an attribute of a filter expression: a valid attribute that is
// not a reserved word and does not start with a geo function the grammar matches by prefix (`_geoPoint(`, `_geoDistance(`).
func validFilterAttribute(name string) bool {
	if !validAttribute(name) {
		return false
	}

	if _, reserved := reservedFilterWords[name]; reserved {
		return false
	}

	return !strings.HasPrefix(name, "_geoPoint") && !strings.HasPrefix(name, "_geoDistance")
}

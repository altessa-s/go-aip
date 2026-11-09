// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"regexp"
	"strings"

	"github.com/altessa-s/go-aip/filter/regexanchor"
)

// anchorCacheSize bounds the patterns whose end-of-text rewrite is cached.
// The rewrite is a pure function of the pattern, so the cache is shared by
// every translator.
const anchorCacheSize = 1024

var anchorCache = newPatternCache(anchorCacheSize)

// endOfText is [regexanchor.EndOfText] memoized per pattern: the rewrite
// re-parses the pattern twice to verify itself, which dominates a translate.
// Only successful rewrites are cached.
func endOfText(pattern string) (string, error) {
	if !strings.Contains(pattern, "$") {
		return pattern, nil
	}

	if out, ok := anchorCache.get(pattern); ok {
		return out, nil
	}

	out, err := regexanchor.EndOfText(pattern)
	if err != nil {
		return "", err
	}

	anchorCache.put(pattern, out)

	return out, nil
}

// regexTransform transforms a string argument into a regex pattern.
// Returns the pattern and an error if validation fails.
type regexTransform func(string) (string, error)

// The anchors are chosen for MongoDB's PCRE engine, which reads `$` as "the
// end, or just before a final newline": endsWith uses `\z`, the absolute end,
// so "abc\n" does not end with "abc". `^` needs no such care — without the m
// option it matches only at the start.
func regexContains(s string) (string, error)   { return quoteLiteral(s), nil }
func regexStartsWith(s string) (string, error) { return "^" + quoteLiteral(s), nil }
func regexEndsWith(s string) (string, error)   { return quoteLiteral(s) + `\z`, nil }

// quoteLiteral escapes s into a pattern matching it literally (see escapeNUL).
func quoteLiteral(s string) string {
	return escapeNUL(regexp.QuoteMeta(s))
}

// escapeNUL spells every raw NUL byte of a pattern as `\x00`: a BSON regex is a
// C string, so MongoDB rejects a raw NUL byte. A raw NUL in a pattern that
// passed RE2 validation is always a literal (RE2 rejects a backslash before
// it). Outside quoting, and in a character class, it becomes `\x00`; inside
// \Q…\E, where `\x00` would read as four literal characters, the quote is
// closed around it (`\E\x00\Q`), and an unterminated \Q is reopened the same
// way. The rewrite never changes what the pattern matches.
func escapeNUL(pattern string) string {
	if !strings.Contains(pattern, "\x00") {
		return pattern
	}

	var b strings.Builder
	b.Grow(len(pattern) + len(`\E\x00\Q`))

	quoted := false

	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case quoted:
			switch {
			case c == 0:
				b.WriteString(`\E\x00\Q`)
			case c == '\\' && i+1 < len(pattern) && pattern[i+1] == 'E':
				b.WriteString(`\E`)

				i++
				quoted = false
			default:
				b.WriteByte(c)
			}
		case c == 0:
			b.WriteString(`\x00`)
		case c == '\\' && i+1 < len(pattern):
			// An escape: copy it whole so an escaped backslash is not read
			// as the start of another one; \Q opens quoting.
			b.WriteByte(c)
			b.WriteByte(pattern[i+1])
			quoted = pattern[i+1] == 'Q'
			i++
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

// regexPassthrough validates through the configured policy. Default RE2 patterns
// receive end-of-text anchor rewrites; custom compiler patterns retain their
// backend syntax. Both escape NUL for BSON transport.
func (t *FilterTranslator) regexPassthrough(s string) (string, error) {
	if _, err := t.config.CompileRegex(s); err != nil {
		return "", err
	}

	if !t.config.UsesRE2Regex() {
		return escapeNUL(s), nil
	}

	pattern, err := endOfText(s)
	if err != nil {
		return "", err
	}

	return escapeNUL(pattern), nil
}

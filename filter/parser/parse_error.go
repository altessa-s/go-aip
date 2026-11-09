// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"cel.dev/cel-go/cel"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Bounds of a parse error, which reaches API responses and logs: cel-go renders every syntax error with the whole source line
// and a caret line, up to 100 of them, so a 4 KB filter of mismatched parentheses yields an error of about 443 KB.
const (
	// errorRecoveryLimit is how many times the CEL parser recovers from a syntax error and goes on looking for more; only the
	// first is reported, so recovering further is wasted work.
	errorRecoveryLimit = 4
	// maxErrorMessage is the most bytes of the first error's message a parse error keeps; a token recognition error echoes
	// the token, which can be most of the expression.
	maxErrorMessage = 160
	// snippetRadius is how many characters of the expression a parse error shows on each side of the error.
	snippetRadius = 24
)

// parseError returns the compact [filter.ErrParseFailed] error of a failed parse of expression: the position and message of
// the first syntax error, a short quoted snippet of the expression around it, and how many more errors there were. Its text
// is bounded by about 600 bytes whatever the expression.
func parseError(expression string, errs []*cel.Error) error {
	if len(errs) == 0 {
		return filter.ErrParseFailed
	}

	first := errs[0]

	var b strings.Builder

	if loc := first.Location; loc != nil && loc.Line() > 0 {
		b.WriteString(strconv.Itoa(loc.Line()))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(loc.Column() + 1))
		b.WriteString(": ")
		b.WriteString(truncate(first.Message, maxErrorMessage))
		b.WriteString(" near ")
		b.WriteString(strconv.Quote(snippet(expression, loc.Line(), loc.Column())))
	} else {
		b.WriteString(truncate(first.Message, maxErrorMessage))
	}

	if more := len(errs) - 1; more > 0 {
		b.WriteString(" (and ")
		b.WriteString(strconv.Itoa(more))
		b.WriteString(" more)")
	}

	return coreerrs.Wrapf(filter.ErrParseFailed, "%s", b.String())
}

// snippet returns up to snippetRadius characters of expression on each side of the 0-based column of the 1-based line.
func snippet(expression string, line, column int) string {
	for range line - 1 {
		_, rest, ok := strings.Cut(expression, "\n")
		if !ok {
			return ""
		}

		expression = rest
	}

	text, _, _ := strings.Cut(expression, "\n")

	lo, hi := max(column-snippetRadius, 0), column+snippetRadius
	start, end, n := len(text), len(text), 0

	for i := range text { // i is the byte offset of the n-th character
		if n == lo {
			start = i
		}

		if n == hi {
			end = i
			break
		}

		n++
	}

	return text[start:end]
}

// truncate returns s cut to at most limit bytes on a rune boundary, with "..." appended when it was cut.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut] + "..."
}

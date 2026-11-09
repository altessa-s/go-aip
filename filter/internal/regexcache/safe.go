// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache

import (
	"errors"
	"fmt"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
)

// Bounds of a safe pattern ([Safe]).
const (
	// MaxUnboundedRepeats is the number of unbounded repetitions (*, +, {n,}) a safe pattern may hold.
	MaxUnboundedRepeats = 2
	// MaxRepeats is the number of repetitions of a variable count (*, +, ?, {n,m} with n < m, {n,}) a safe pattern may hold.
	MaxRepeats = 4
	// MaxRepeatCount is the largest bound of a counted repetition ({n}, {n,m}, {n,}) in a safe pattern.
	MaxRepeatCount = 100
	// MaxRepeatProduct is the largest product of the number of choices of a safe pattern: the branches of each alternation
	// (a|b|c has 3) and the repeat counts (m-n+1, 2 for ?) of each bounded repetition of a variable count (?, {n,m} with n < m).
	// It bounds the ways a backtracking engine may try one attempt: (a|a)(a|a)$ already takes 4 on a failing row.
	MaxRepeatProduct = 1000
)

var (
	// errNestedRepeat reports a repetition of something other than one character.
	errNestedRepeat = errors.New("a repetition of a group, an alternation or a repetition is not allowed")
	// errLiteralBrace reports a { that the Go parser reads as a literal and a backtracking engine may read as a repetition.
	errLiteralBrace = errors.New(`a { that does not start a repetition {n}, {n,} or {n,m} is not allowed; write \{`)
	// errOctalEscape reports an escape \1 to \7 that the Go parser reads as an octal character and a backtracking engine may read as
	// a back-reference.
	errOctalEscape = errors.New(`an octal escape \1 to \7 is not allowed; write \x{...}`)
	// errChoices reports alternations and bounded repetitions whose choices multiply past [MaxRepeatProduct].
	errChoices = fmt.Errorf("alternations and bounded repetitions with more than %d combined choices are not allowed", MaxRepeatProduct)
)

// Safe reports why a backtracking engine (MongoDB PCRE, PostgreSQL ~, MariaDB REGEXP, MySQL ICU) may take more than polynomial
// time of a bounded degree, or a large constant factor, to run pattern, read with the Perl syntax [regexp.Compile] reads; nil when
// it is safe.
//
// A safe pattern repeats (*, +, ?, {n}, {n,m}, {n,}) only a single character, a character class or the any-character dot, never
// a parenthesized group, an alternation or another repetition (the exponential shapes, such as (a|aa)+). The check reads the
// source, not only the parsed tree: the Go parser folds (?:a|a)* and (?:.|[^!])* into one repeated character class, while a
// backtracking engine still tries every branch of the alternation at every step. A safe pattern holds at most
// [MaxUnboundedRepeats] unbounded and [MaxRepeats] variable repetitions (the degree of the polynomial), no counted bound above
// [MaxRepeatCount], and alternations and bounded variable repetitions whose choices multiply to at most [MaxRepeatProduct] (the
// constant): the branches are counted from the source too, since the parser folds (a|a) and (?:.|a) into one character. No two
// unbounded repetitions over characters they share may follow each other with only parts that can match the empty string between
// (.*.*, a+a*, \d+\.?\d*, (?:a*|b)a*): they split a run between them in every way. Lastly, a safe pattern spells out what the
// engines read differently: a literal { is escaped, and an octal character is not written \1 to \7.
func Safe(pattern string) error {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return err
	}

	branches, err := checkSource(pattern)
	if err != nil {
		return err
	}

	c := safety{product: branches}
	c.walk(re)

	return c.err
}

// checkSource scans pattern, which the Go parser accepts, for what its parsed tree no longer shows: a repetition of a parenthesized
// group, a literal { and an octal escape. It returns the product of the branch counts of every alternation, the top level
// included: the parser folds (a|a) and (?:.|a) into one character and factors abc|abd into ab[cd], while a backtracking engine
// tries every branch, so a chain of k two-way groups takes 2^k tries on a failing row.
func checkSource(pattern string) (int, error) {
	product := 1
	branches := []int{1} // the branches of each open group, the top level first

	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '\\':
			if i+1 < len(pattern) && pattern[i+1] >= '1' && pattern[i+1] <= '7' {
				return 0, errOctalEscape
			}

			i = skipEscape(pattern, i, true)
		case '[':
			i = skipClass(pattern, i)
		case '(':
			branches = append(branches, 1)
			i++
		case '|':
			branches[len(branches)-1]++
			i++
		case ')':
			if rest := pattern[i+1:]; rest != "" && (strings.IndexByte("*+?", rest[0]) >= 0 || repeatLen(rest) > 0) {
				return 0, errNestedRepeat
			}

			if len(branches) > 1 { // the parser accepted pattern, so every ) closes a group
				if product *= branches[len(branches)-1]; product > MaxRepeatProduct {
					return 0, errChoices
				}

				branches = branches[:len(branches)-1]
			}

			i++
		case '{':
			n := repeatLen(pattern[i:])
			if n == 0 {
				return 0, errLiteralBrace
			}

			i += n
		default:
			i++
		}

		// The count of an open group only grows, so checking it as it grows rejects a long alternation early.
		if product*branches[len(branches)-1] > MaxRepeatProduct {
			return 0, errChoices
		}
	}

	return product * branches[0], nil
}

// skipEscape returns the index past the escape at pattern[i] (a backslash): \Q...\E at the top level, \p{...}, \P{...}, \x{...},
// \pL and \PL with a one-letter class name, \x41 with two hex digits, or a backslash and one more byte.
func skipEscape(pattern string, i int, top bool) int {
	const escape = len(`\x`) // a backslash and the byte it escapes

	if i+1 >= len(pattern) {
		return len(pattern)
	}

	body := i + escape

	switch pattern[i+1] {
	case 'Q':
		if !top {
			break
		}

		end := strings.Index(pattern[body:], `\E`)
		if end < 0 {
			return len(pattern)
		}

		return body + end + len(`\E`)
	case 'p', 'P', 'x':
		if body < len(pattern) && pattern[body] == '{' {
			if end := strings.IndexByte(pattern[body:], '}'); end >= 0 {
				return body + end + 1
			}

			return len(pattern)
		}

		if pattern[i+1] == 'x' {
			return min(body+len("41"), len(pattern)) // two hex digits: \x41
		}

		return min(body+1, len(pattern)) // a one-letter class name: \pL, \PC
	default:
	}

	return body
}

// skipClass returns the index past the character class at pattern[i] (an opening bracket), read as the Go parser reads it: a ]
// first is a literal, [:name:] is a named class, and a backslash escapes the next byte.
func skipClass(pattern string, i int) int {
	j := i + 1
	if j < len(pattern) && pattern[j] == '^' {
		j++
	}

	if j < len(pattern) && pattern[j] == ']' {
		j++
	}

	for j < len(pattern) {
		switch pattern[j] {
		case '\\':
			j = skipEscape(pattern, j, false)
		case '[':
			if strings.HasPrefix(pattern[j:], "[:") {
				if end := strings.Index(pattern[j+2:], ":]"); end >= 0 {
					j += len("[:") + end + len(":]")
					continue
				}
			}

			j++
		case ']':
			return j + 1
		default:
			j++
		}
	}

	return j
}

// repeatLen returns the length of the counted repetition {n}, {n,} or {n,m} that s starts with, as the Go parser reads it; 0 when
// s does not start with one.
func repeatLen(s string) int {
	if s == "" || s[0] != '{' {
		return 0
	}

	i := 1
	digits := func() int {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}

		return i - start
	}

	if digits() == 0 {
		return 0
	}

	if i < len(s) && s[i] == ',' {
		i++

		digits()
	}

	if i < len(s) && s[i] == '}' {
		return i + 1
	}

	return 0
}

// safety is the state of a [Safe] check: the repetitions counted so far, the product of the choices of the bounded variable ones
// and the first violation.
type safety struct {
	unbounded, variable, product int
	err                          error
}

// walk checks re and its parts.
func (c *safety) walk(re *syntax.Regexp) {
	if c.err != nil {
		return
	}

	switch re.Op {
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		c.repeat(re)
		return
	case syntax.OpConcat:
		c.adjacent(re.Sub)
	default:
	}

	for _, sub := range re.Sub {
		c.walk(sub)
	}
}

// repeat checks and counts the repetition re.
func (c *safety) repeat(re *syntax.Regexp) {
	switch {
	case !isSingleChar(re.Sub[0]):
		c.err = errNestedRepeat
	case re.Op == syntax.OpRepeat && max(re.Min, re.Max) > MaxRepeatCount:
		c.err = fmt.Errorf("a repetition count above %d is not allowed", MaxRepeatCount)
	case !variable(re):
	case unbounded(re):
		c.unbounded++
		c.variable++
	default:
		c.variable++
		c.product *= choices(re)
	}

	switch {
	case c.err != nil:
	case c.unbounded > MaxUnboundedRepeats:
		c.err = fmt.Errorf("more than %d unbounded repetitions are not allowed", MaxUnboundedRepeats)
	case c.variable > MaxRepeats:
		c.err = fmt.Errorf("more than %d repetitions of a variable count are not allowed", MaxRepeats)
	case c.product > MaxRepeatProduct:
		c.err = errChoices
	default:
	}
}

// adjacent checks the parts of a concatenation, in order, for an unbounded repetition followed by another over a character both
// match, with only parts that can match the empty string between, looking into groups and the branches of alternations.
func (c *safety) adjacent(parts []*syntax.Regexp) {
	var open []*syntax.Regexp // unbounded repetitions that may have matched the last character so far

	for _, part := range parts {
		e := endsOf(part)

		for _, prev := range open {
			for _, next := range e.first {
				if overlap(charRanges(prev.Sub[0]), charRanges(next.Sub[0])) {
					c.err = errors.New("two unbounded repetitions in a row over the same characters are not allowed")
					return
				}
			}
		}

		if e.nullable {
			open = append(open, e.last...)
		} else {
			open = slices.Clone(e.last)
		}
	}
}

// ends describes a part of a concatenation for [safety.adjacent]: the unbounded repetitions that may match its first and its last
// character, and whether it may match the empty string.
type ends struct {
	first, last []*syntax.Regexp
	nullable    bool
}

// endsOf returns the [ends] of re.
func endsOf(re *syntax.Regexp) ends {
	switch re.Op {
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		e := ends{nullable: re.Op == syntax.OpStar || re.Op == syntax.OpQuest || re.Op == syntax.OpRepeat && re.Min == 0}
		if unbounded(re) {
			e.first, e.last = []*syntax.Regexp{re}, []*syntax.Regexp{re}
		}

		return e
	case syntax.OpCapture:
		return endsOf(re.Sub[0])
	case syntax.OpConcat:
		e := ends{nullable: true}

		for _, sub := range re.Sub {
			s := endsOf(sub)
			if e.nullable {
				e.first = append(e.first, s.first...)
			}

			if s.nullable {
				e.last = append(e.last, s.last...)
			} else {
				e.last = slices.Clone(s.last)
			}

			e.nullable = e.nullable && s.nullable
		}

		return e
	case syntax.OpAlternate:
		var e ends

		for _, sub := range re.Sub {
			s := endsOf(sub)
			e.first = append(e.first, s.first...)
			e.last = append(e.last, s.last...)
			e.nullable = e.nullable || s.nullable
		}

		return e
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return ends{nullable: true}
	default:
		return ends{}
	}
}

// unbounded reports whether re is a repetition without an upper bound: *, + or {n,}.
func unbounded(re *syntax.Regexp) bool {
	return re.Op == syntax.OpStar || re.Op == syntax.OpPlus || re.Op == syntax.OpRepeat && re.Max == -1
}

// variable reports whether the repetition re may repeat a varying number of times: anything but {n}.
func variable(re *syntax.Regexp) bool {
	return re.Op != syntax.OpRepeat || re.Min != re.Max
}

// choices returns the number of repeat counts the bounded variable repetition re (? or {n,m} with n < m) may take.
func choices(re *syntax.Regexp) int {
	const quest = 2 // a ? repeats zero times or once

	if re.Op == syntax.OpQuest {
		return quest
	}

	return re.Max - re.Min + 1
}

// isSingleChar reports whether re matches one character: a literal rune, a character class or the any-character dot.
func isSingleChar(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune) == 1
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	default:
		return false
	}
}

// charRanges returns the characters re matches as inclusive rune ranges (lo, hi pairs) when re matches one character
// ([isSingleChar]): a literal rune and its case folds, a character class or the any-character dot; nil otherwise.
func charRanges(re *syntax.Regexp) []rune {
	switch re.Op {
	case syntax.OpLiteral:
		if len(re.Rune) != 1 {
			return nil
		}

		r := re.Rune[0]

		ranges := []rune{r, r}
		if re.Flags&syntax.FoldCase != 0 {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				ranges = append(ranges, f, f)
			}
		}

		return ranges
	case syntax.OpCharClass:
		return re.Rune
	case syntax.OpAnyChar:
		return []rune{0, unicode.MaxRune}
	case syntax.OpAnyCharNotNL:
		return []rune{0, '\n' - 1, '\n' + 1, unicode.MaxRune}
	default:
		return nil
	}
}

// overlap reports whether two sets of rune ranges ([charRanges]) share a character.
func overlap(a, b []rune) bool {
	for i := 0; i+1 < len(a); i += 2 {
		for j := 0; j+1 < len(b); j += 2 {
			if a[i] <= b[j+1] && b[j] <= a[i+1] {
				return true
			}
		}
	}

	return false
}

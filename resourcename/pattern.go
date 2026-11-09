// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Pattern is a compiled resource name pattern of AIP-123, such as
// "publishers/{publisher}/books/{book}": collection identifiers, each
// optionally followed by a {variable} holding a resource ID. A collection
// identifier not followed by a variable is a singleton (AIP-156), such as the
// "config" of "users/{user}/config".
//
// Matching walks the name once without regular expressions or allocations;
// it is structural only: literal segments must be equal and variable
// segments non-empty. The ID policy, the "-" wildcard of AIP-159 and the
// limits are applied by a [Validator].
//
// A Pattern is immutable and safe for concurrent use.
type Pattern struct {
	raw  string
	segs []segment
	vars []string
}

// segment is one segment of a pattern: a literal collection identifier, or
// a variable when variable is not empty.
type segment struct {
	literal  string
	variable string
}

// ParsePattern compiles a resource name pattern. The pattern must follow
// AIP-122 and AIP-123:
//
//   - segments are separated by "/", none is empty, and the first one is a
//     collection identifier;
//   - a collection identifier starts with a lower-case ASCII letter and holds
//     only ASCII letters and digits (camelCase), and is unique in the pattern;
//   - a variable is written {name}, follows a collection identifier, matches
//     [a-z][_a-z0-9]*[a-z0-9] (snake_case), has no "_id" suffix and is unique
//     in the pattern.
//
// Path-template forms such as {name=**} are not resource name patterns and
// are refused. Errors match [ErrInvalidPattern].
func ParsePattern(pattern string) (*Pattern, error) {
	if pattern == "" {
		return nil, patternError(pattern, "is empty")
	}

	parts := strings.Split(pattern, "/")
	p := &Pattern{raw: pattern, segs: make([]segment, 0, len(parts))}
	literals := make(map[string]struct{}, len(parts))

	for i, part := range parts {
		if part == "" {
			return nil, patternError(pattern, "has an empty segment")
		}

		if part[0] != '{' {
			if !validCollection(part) {
				return nil, patternError(pattern, fmt.Sprintf("segment %q is not a camelCase collection identifier", part))
			}

			if _, dup := literals[part]; dup {
				return nil, patternError(pattern, fmt.Sprintf("collection identifier %q repeats", part))
			}

			literals[part] = struct{}{}
			p.segs = append(p.segs, segment{literal: part})

			continue
		}

		name, ok := strings.CutSuffix(part[1:], "}")
		if !ok {
			return nil, patternError(pattern, fmt.Sprintf("segment %q is not a {variable}", part))
		}

		if i == 0 || p.segs[i-1].variable != "" {
			return nil, patternError(pattern, fmt.Sprintf("variable %q does not follow a collection identifier", name))
		}

		if !validVariable(name) {
			return nil, patternError(pattern, fmt.Sprintf("variable %q is not snake_case [a-z][_a-z0-9]*[a-z0-9]", name))
		}

		if strings.HasSuffix(name, "_id") {
			return nil, patternError(pattern, fmt.Sprintf("variable %q has an _id suffix", name))
		}

		if slices.Contains(p.vars, name) {
			return nil, patternError(pattern, fmt.Sprintf("variable %q repeats", name))
		}

		p.vars = append(p.vars, name)
		p.segs = append(p.segs, segment{variable: name})
	}

	return p, nil
}

// MustParsePattern is [ParsePattern] that panics on an invalid pattern. Use
// it for patterns known at compile time.
func MustParsePattern(pattern string) *Pattern {
	p, err := ParsePattern(pattern)
	if err != nil {
		panic(err)
	}

	return p
}

func patternError(pattern, reason string) error {
	return fmt.Errorf("%w %q: %s", ErrInvalidPattern, pattern, reason)
}

// String returns the pattern as written.
func (p *Pattern) String() string { return p.raw }

// Variables returns the variable names in the order they appear.
func (p *Pattern) Variables() []string { return slices.Clone(p.vars) }

// Segments returns the number of segments a matching name has.
func (p *Pattern) Segments() int { return len(p.segs) }

// Singleton reports whether the pattern ends with a collection identifier
// instead of a variable: it names a singleton resource (AIP-156).
func (p *Pattern) Singleton() bool { return p.segs[len(p.segs)-1].variable == "" }

// Matches reports whether name matches the pattern.
func (p *Pattern) Matches(name string) bool {
	var buf [8]string

	_, ok := p.appendValues(buf[:0], name)

	return ok
}

// Match returns the variable values of a name matching the pattern. An error
// is a *[NameError] matching [ErrNoMatch] that names the first offending
// segment.
func (p *Pattern) Match(name string) (map[string]string, error) {
	var buf [8]string

	vals, ok := p.appendValues(buf[:0], name)
	if !ok {
		return nil, p.explain(name)
	}

	m := make(map[string]string, len(vals))
	for i, v := range vals {
		m[p.vars[i]] = v
	}

	return m, nil
}

// AppendValues appends the variable values of a name matching the pattern to
// dst, in the order of [Pattern.Variables], and returns the extended slice.
// The values are substrings of name, so a dst with enough capacity makes the
// call allocation-free. On a mismatch it returns dst unchanged and a
// *[NameError] matching [ErrNoMatch].
func (p *Pattern) AppendValues(dst []string, name string) ([]string, error) {
	vals, ok := p.appendValues(dst, name)
	if !ok {
		return dst, p.explain(name)
	}

	return vals, nil
}

// appendValues is the allocation-free matcher behind every match method.
func (p *Pattern) appendValues(dst []string, name string) ([]string, bool) {
	base, rest := len(dst), name
	last := len(p.segs) - 1

	for i, s := range p.segs {
		var seg string

		if i == last {
			if strings.IndexByte(rest, '/') >= 0 {
				return dst[:base], false
			}

			seg = rest
		} else {
			j := strings.IndexByte(rest, '/')
			if j < 0 {
				return dst[:base], false
			}

			seg, rest = rest[:j], rest[j+1:]
		}

		switch {
		case s.variable == "":
			if seg != s.literal {
				return dst[:base], false
			}
		case seg == "":
			return dst[:base], false
		default:
			dst = append(dst, seg)
		}
	}

	return dst, true
}

// explain returns the *NameError of a name the pattern does not match.
func (p *Pattern) explain(name string) *NameError {
	if n := segmentCount(name); n != len(p.segs) {
		return nameError(ErrNoMatch, -1, fmt.Sprintf("the name has %d segments, pattern %q has %d", n, p.raw, len(p.segs)))
	}

	i := 0

	for seg := range strings.SplitSeq(name, "/") {
		s := p.segs[i]

		switch {
		case s.variable == "" && seg != s.literal:
			return nameError(ErrNoMatch, i, fmt.Sprintf("segment %d must be %q (pattern %q)", i, s.literal, p.raw))
		case s.variable != "" && seg == "":
			return nameError(ErrNoMatch, i, fmt.Sprintf("segment %d, the %s ID, is empty (pattern %q)", i, s.variable, p.raw))
		}

		i++
	}

	return nameError(ErrNoMatch, -1, fmt.Sprintf("the name does not match pattern %q", p.raw))
}

// Format returns the name the pattern makes from values, keyed by variable
// name. Every variable needs a non-empty value without "/": AIP-122 names
// are never URL-escaped, so a value that cannot be a segment is refused
// rather than escaped. A key that is not a variable of the pattern is
// refused too. Errors match [ErrInvalidValue].
func (p *Pattern) Format(values map[string]string) (string, error) {
	for k := range values {
		if !slices.Contains(p.vars, k) {
			return "", fmt.Errorf("%w: %q is not a variable of pattern %q", ErrInvalidValue, k, p.raw)
		}
	}

	return p.format(func(i int) (string, bool) {
		v, ok := values[p.vars[i]]

		return v, ok
	})
}

// FormatValues is [Pattern.Format] with the values in the order of
// [Pattern.Variables].
func (p *Pattern) FormatValues(values ...string) (string, error) {
	if len(values) != len(p.vars) {
		return "", fmt.Errorf("%w: pattern %q has %d variables, got %d values", ErrInvalidValue, p.raw, len(p.vars), len(values))
	}

	return p.format(func(i int) (string, bool) { return values[i], true })
}

func (p *Pattern) format(value func(i int) (string, bool)) (string, error) {
	var b strings.Builder

	b.Grow(len(p.raw))

	vi := 0

	for i, s := range p.segs {
		if i > 0 {
			b.WriteByte('/')
		}

		if s.variable == "" {
			b.WriteString(s.literal)

			continue
		}

		v, ok := value(vi)

		switch {
		case !ok:
			return "", fmt.Errorf("%w: no value for %q of pattern %q", ErrInvalidValue, s.variable, p.raw)
		case v == "":
			return "", fmt.Errorf("%w: empty value for %q of pattern %q", ErrInvalidValue, s.variable, p.raw)
		case strings.IndexByte(v, '/') >= 0:
			return "", fmt.Errorf("%w: value for %q of pattern %q holds \"/\"", ErrInvalidValue, s.variable, p.raw)
		}

		b.WriteString(v)

		vi++
	}

	return b.String(), nil
}

// Parent returns the pattern of the parent resource: the pattern without its
// last collection identifier and variable, or without its trailing singleton
// identifier. A top-level pattern such as "publishers/{publisher}" has the
// root as parent, and Parent returns false.
func (p *Pattern) Parent() (*Pattern, bool) {
	n := len(p.segs) - 1
	if p.segs[n].variable != "" {
		n--
	}

	if n <= 0 {
		return nil, false
	}

	segs := p.segs[:n]
	parts := make([]string, len(segs))
	vars := 0

	for i, s := range segs {
		if s.variable == "" {
			parts[i] = s.literal
		} else {
			parts[i] = "{" + s.variable + "}"
			vars++
		}
	}

	return &Pattern{raw: strings.Join(parts, "/"), segs: segs, vars: p.vars[:vars:vars]}, true
}

// shape is the pattern with its variables erased: two patterns of the same
// shape match the same names.
func (p *Pattern) shape() string {
	var b strings.Builder

	for i, s := range p.segs {
		if i > 0 {
			b.WriteByte('/')
		}

		if s.variable == "" {
			b.WriteString(s.literal)
		} else {
			b.WriteString("{}")
		}
	}

	return b.String()
}

// lastVariable is the index of the last variable among the values, or -1.
func (p *Pattern) lastVariable() int { return len(p.vars) - 1 }

// variableSegment is the segment index of the i-th variable.
func (p *Pattern) variableSegment(i int) int {
	for j, s := range p.segs {
		if s.variable != "" {
			if i == 0 {
				return j
			}

			i--
		}
	}

	return -1
}

// segmentCount counts the "/"-separated segments of name.
func segmentCount(name string) int { return strings.Count(name, "/") + 1 }

// validCollection reports whether s is a collection identifier of AIP-122:
// a lower-case ASCII letter followed by ASCII letters and digits.
func validCollection(s string) bool {
	if s == "" || !isLower(s[0]) {
		return false
	}

	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLower(c) && !isUpper(c) && !isDigit(c) {
			return false
		}
	}

	return true
}

// validVariable reports whether s matches [a-z][_a-z0-9]*[a-z0-9] (AIP-123).
func validVariable(s string) bool {
	if len(s) < 2 || !isLower(s[0]) || s[len(s)-1] == '_' {
		return false
	}

	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLower(c) && !isDigit(c) && c != '_' {
			return false
		}
	}

	return true
}

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// quoteList renders patterns for an error reason.
func quoteList(ps []*Pattern) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = strconv.Quote(p.raw)
	}

	return strings.Join(parts, ", ")
}

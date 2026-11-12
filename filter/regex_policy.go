// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter/internal/regexcache"

	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// checkRegexCall applies the matches() policy ([Policy.CompileRegex]) to a call node whose pattern is a string literal.
// Under [WithUntrustedInput] the pattern must be one; otherwise the translator reports another argument, and the evaluator checks
// the pattern it computes. Every other call passes.
func (o *Policy) checkRegexCall(n *CallNode) error {
	if n.Op != OpMatches {
		return nil
	}

	if o.untrustedInput && !o.safeRegex {
		return coreerrs.Wrap(ErrFunctionNotAllowed, "matches() on untrusted input")
	}

	if o.unsafeRegex {
		return nil
	}

	if len(n.Args) != 1 {
		return nil // the translator reports the malformed call
	}

	lit, ok := n.Args[0].(*LiteralNode)
	if !ok || lit == nil {
		if o.untrustedInput {
			return coreerrs.Wrap(regexanchor.ErrInvalidRegex, "matches() on untrusted input takes a string literal pattern")
		}

		return nil
	}

	pattern, ok := lit.Value.(string)
	if !ok {
		return nil // the translator reports the non-string argument
	}

	_, err := o.CompileRegex(pattern)

	return err
}

// CompileRegex applies the matches() policy to pattern and returns it compiled, from the cache when it holds it. Under
// [WithUntrustedInput] without [WithSafeRegex] no pattern passes ([ErrFunctionNotAllowed]). A pattern longer than the regex length
// limit ([WithMaxRegexLength]), one that does not compile, or — unless [WithUnsafeRegex] — one outside the safe subset
// ([regexcache.Safe]) fails with [regexanchor.ErrInvalidRegex]. The subset is judged once per cached pattern.
func (o *Policy) CompileRegex(pattern string) (Regex, error) {
	if o.untrustedInput && !o.safeRegex {
		return Regex{}, coreerrs.Wrap(ErrFunctionNotAllowed, "matches() on untrusted input")
	}

	if maxLen := o.MaxRegexLength(); len(pattern) > maxLen {
		return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "pattern length %d exceeds maximum %d", len(pattern), maxLen)
	}

	if o.regexValidator != nil && !o.unsafeRegex {
		if err := o.regexValidator(pattern); err != nil {
			return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "%w", err)
		}
	}

	if o.regexCompiler != nil {
		if !o.unsafeRegex && o.regexValidator == nil {
			if err := regexcache.Safe(pattern); err != nil {
				return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "%w", err)
			}
		}

		matcher, err := o.regexCompiler(pattern)
		if err != nil {
			return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "%w", err)
		}

		if nilcheck.IsNil(matcher) {
			return Regex{}, regexanchor.ErrInvalidRegex
		}

		return Regex{compiled: matcher}, nil
	}

	p, err := o.regexCache.Lookup(pattern)
	if err != nil {
		return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "%w", err)
	}

	if o.unsafeRegex || o.regexValidator != nil {
		return Regex{compiled: p.Regexp()}, nil
	}

	if err = p.Safe(); err != nil {
		return Regex{}, coreerrs.Wrapf(regexanchor.ErrInvalidRegex, "%w (WithUnsafeRegex lifts the safe subset)", err)
	}

	return Regex{compiled: p.Regexp()}, nil
}

// checkRegexOptions rejects [WithUnsafeRegex] combined with [WithUntrustedInput]: a client pattern is always held to the safe
// subset.
func (o *Policy) checkRegexOptions() error {
	if o.unsafeRegex && o.untrustedInput {
		return coreerrs.Wrap(ErrInvalidOptions, "WithUnsafeRegex cannot be combined with WithUntrustedInput")
	}

	return nil
}

// RegexMatcher is the immutable matching result of an injected compiler.
type RegexMatcher interface{ MatchString(string) bool }

// Regex is an immutable matcher admitted by a policy.
type Regex struct{ compiled RegexMatcher }

// MatchString reports whether the pattern matches s. A zero Regex matches nothing.
func (r Regex) MatchString(s string) bool { return r.compiled != nil && r.compiled.MatchString(s) }

// UsesRE2Regex reports whether the policy uses the built-in RE2 compiler.
// Backend translators use this to apply RE2-specific syntax rewrites only to
// default patterns. Custom compilers own their backend-compatible semantics.
func (o *Policy) UsesRE2Regex() bool { return o.regexCompiler == nil }

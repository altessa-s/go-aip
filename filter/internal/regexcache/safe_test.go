// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/internal/regexcache"
)

// TestSafe_Accepted pins common patterns that stay inside the safe subset.
func TestSafe_Accepted(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		`abc`,
		`^abc`,
		`abc$`,
		`^abc$`,
		`^(foo|bar)$`,
		`^(?:foo|bar)baz$`,
		`(?i)a\.b`,
		`(?i)^hello`,
		`^[a-z]+@example\.(com|org)$`,
		`^https?://[^/]+/.*$`,
		`^[a-z]*[0-9]+$`,
		`^\d+\.\d+$`,
		`^[A-Z][a-z]+$`,
		`^[a-z]+-[0-9]{2,4}.*x$`,
		`^\+?\d{1,3}-\d{3}-\d{4}$`,
		`^\d{4}-\d{2}-\d{2}$`,
		`^A.*e$`,
		`^.+$`,
		`a{100}`,
		`a{0,100}b*`,
		`a*b*`,
		`(?i)a*B*`,
		`a?b?c?d?`,
		`[ab]{0,30}c[ab]{0,30}`,
		`.*\n.*`,
		`\{\}`,
		`\Q(a|a)*{,5}\E`,
		`[(|)*{]`,
		`[]{]x`,
		`[[:alpha:]]+`,
		`\p{Greek}+`,
		`\x{41}+`,
		`\0`,
		`\bword\b`,
		`^(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)-(?:mon|tue|wed|thu|fri|sat|sun)$`,
		`^(?:a|b)(?:c|d)(?:e|f)(?:g|h)(?:i|j)(?:k|l)(?:m|n)(?:o|p)(?:q|r)$`,
		`^(?:foo|bar)[a-z]{0,100}$`,
		`(?i)^(?:http|https|ftp)://`,
	} {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, regexcache.Safe(pattern))
		})
	}
}

// TestSafe_Rejected pins the shapes outside the safe subset, the reported ReDoS inputs included, with the reason for each.
func TestSafe_Rejected(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern, reason string
	}{
		{`(a|aa)+`, "repeated alternation"},
		{`(a+)+`, "nested repetition"},
		{`(ab)*`, "repeated group"},
		{`^(?:a|a)*b`, "overlapping alternation the Go parser folds into a*"},
		{`^(?:.|[^!])*!`, "overlapping alternation the Go parser folds into a class"},
		{`^(?:\w|\d)*$`, "overlapping alternation the Go parser folds into a class"},
		{`^(?:x|.)+$`, "overlapping alternation the Go parser folds into a class"},
		{`(?:a|a){100}`, "alternation under a fixed count: 2^100 paths"},
		{`(?:a){2}`, "repeated group"},
		{`(a){1,3}`, "repeated group"},
		{`(?:a*)*`, "nested repetition"},
		{`((a))?`, "repeated nested group"},
		{`[ab]{0,100}[ab]{0,100}[ab]{0,100}[ac]$`, "bounded choices multiply past the cap"},
		{`.{0,100}x.{0,100}`, "bounded choices multiply past the cap"},
		{`x+y+z+`, "too many unbounded repetitions"},
		{`^[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`, "too many unbounded repetitions"},
		{`a?b?c?d?e?`, "too many variable repetitions"},
		{`a{1,101}`, "count above the cap"},
		{`a{101,}`, "count above the cap"},
		{`.*.*`, "adjacent overlapping unbounded repetitions"},
		{`a+a*`, "adjacent overlapping unbounded repetitions"},
		{`(?i)a*A+`, "adjacent overlapping unbounded repetitions under case folding"},
		{`[a-c]+[c-e]+`, "adjacent overlapping unbounded repetitions"},
		{`\d+\.?\d*`, "overlapping unbounded repetitions with an optional part between"},
		{`\w+\b\w+`, "overlapping unbounded repetitions with an assertion between"},
		{`(a*)(a+)`, "adjacent overlapping unbounded repetitions in groups"},
		{`a*b*a*`, "overlapping unbounded repetitions with an optional part between"},
		{`.*(?s:.)*`, "adjacent overlapping unbounded repetitions"},
		{`x(?:a*|b)a*`, "an alternation branch ends with a repetition the next part overlaps"},
		{`(?:b|a+)(?:c|a*)`, "alternation branches meet in overlapping repetitions"},
		{`a{,5}`, "a brace PCRE2 reads as {0,5}"},
		{`(a|aa){,30}`, "a brace PCRE2 reads as a repetition of an alternation"},
		{`a{ 1,3}`, "a brace PCRE2 reads as a repetition"},
		{`x{`, "unescaped literal brace"},
		{`\12`, "octal escape a backtracking engine may read as a back-reference"},
		{`a(?i)*`, "repetition after a flag group"},
		{`(`, "does not parse"},
		{strings.Repeat(`(a|a)`, 25) + "$", "a chain of 25 two-way groups: 2^25 tries"},
		{strings.Repeat(`(?:a|a)`, 25) + "$", "a chain the parser folds into a{25}"},
		{strings.Repeat(`(?:.|a)`, 25) + "$", "a chain the parser folds into .{25}"},
		{strings.Repeat(`(?:.|a)`, 146) + "$", "the longest chain within the default 1024-byte pattern limit"},
		{strings.Repeat(`(?:\w|\d)`, 25) + "$", "a chain the parser folds into classes"},
		{strings.Repeat(`(?:a|)`, 25) + strings.Repeat("a", 25) + "$", "a chain of optional characters written as alternations"},
		{"^" + strings.Repeat(`(?:a|ab|abc)`, 16) + "$", "a chain of three-way groups the parser factors"},
		{strings.Repeat(`(?:a|b)`, 10), "2^10 combined branches"},
		{`^(?:foo|bar)[a-z]{0,500}$`, "branches times bounded repetition choices past the cap"},
		{strings.TrimSuffix(strings.Repeat("a|", 1001), "|"), "a top-level alternation of 1001 branches"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			t.Parallel()

			require.Error(t, regexcache.Safe(tc.pattern), tc.reason)
		})
	}
}

// TestLookup_SafeVerdict pins that Lookup returns the cached pattern with the verdict of Safe, computed once and kept.
func TestLookup_SafeVerdict(t *testing.T) {
	cache, err := regexcache.New(256)
	require.NoError(t, err)
	t.Parallel()

	p, err := cache.Lookup(`^x.*.*$`)
	require.NoError(t, err)
	require.True(t, p.Regexp().MatchString("xyz"))
	first := p.Safe()
	require.Error(t, first)
	require.Same(t, first, p.Safe())

	again, err := cache.Lookup(`^x.*.*$`)
	require.NoError(t, err)
	require.Same(t, p, again)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

// TestSafeRegex_Subset pins S2-2: every matches() pattern is held to the safe subset, with or without [filter.WithUntrustedInput],
// and the subset bounds the polynomial backtracking a database engine may spend on it — the number of unbounded and of variable
// repetitions, the bound of a counted one, and two unbounded repetitions in a row over overlapping characters (.*.*). Each
// pattern is checked by every regex backend and by both evaluators.
func TestSafeRegex_Subset(t *testing.T) {
	t.Parallel()

	trusted := []filter.PolicyOption{filter.WithAllowedFields("name")}
	untrusted := []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("name"), filter.WithSafeRegex(true)}

	for _, tc := range []struct {
		name    string
		pattern string
		want    error
	}{
		{"literal", `abc`, nil},
		{"case-insensitive literal", `(?i)a\\.b`, nil},
		{"alternation of literals", `^(foo|bar)$`, nil},
		{"one unbounded repetition", `^[a-z]+@example\\.(com|org)$`, nil},
		{"two unbounded and an optional", `^https?://[^/]+/.*$`, nil},
		{"bounded next to unbounded", `^[a-z]+-[0-9]{2,4}.*x$`, nil},
		{"unbounded repetitions apart", `^\\d+\\.\\d+$`, nil},
		{"adjacent disjoint repetitions", `^[a-z]*[0-9]+$`, nil},
		{"counted repetition at the bound", `^a{100}$`, nil},
		{"repeated alternation", `(a|aa)+$`, regexanchor.ErrInvalidRegex},
		{"nested quantifier", `(a+)+b`, regexanchor.ErrInvalidRegex},
		{"three unbounded repetitions", `x+y+z+`, regexanchor.ErrInvalidRegex},
		{"too many variable repetitions", `a?b?c?d?e?`, regexanchor.ErrInvalidRegex},
		{"adjacent overlapping repetitions", `.*.*x`, regexanchor.ErrInvalidRegex},
		{"many adjacent dots", `.*.*.*.*.*x`, regexanchor.ErrInvalidRegex},
		{"overlap of a class and its member", `a+a*`, regexanchor.ErrInvalidRegex},
		{"overlap across an optional part", `\\d+\\.?\\d*`, regexanchor.ErrInvalidRegex},
		{"overlap across an empty-width assertion", `\\w+\\b\\w+`, regexanchor.ErrInvalidRegex},
		{"overlap inside groups", `(a*)(a+)`, regexanchor.ErrInvalidRegex},
		{"counted bound above the cap", `a{1,101}`, regexanchor.ErrInvalidRegex},
		{"large counted repetitions", `.{1,1000}.{1,1000}`, regexanchor.ErrInvalidRegex},
		{"alternations in a row", `^(?:jan|feb|mar)-(?:mon|tue|wed)$`, nil},
		// NEW-1: chains of alternations without a quantifier, which the Go parser folds into single characters.
		{"chain of capturing two-way groups", strings.Repeat(`(a|a)`, 25) + "$", regexanchor.ErrInvalidRegex},
		{"chain of folded two-way groups", strings.Repeat(`(?:a|a)`, 25) + "$", regexanchor.ErrInvalidRegex},
		{"chain of dot or a", strings.Repeat(`(?:.|a)`, 25) + "$", regexanchor.ErrInvalidRegex},
		{"chain of word or digit", strings.Repeat(`(?:\\w|\\d)`, 25) + "$", regexanchor.ErrInvalidRegex},
		{"chain of optional characters", strings.Repeat(`(?:a|)`, 25) + strings.Repeat("a", 25) + "$", regexanchor.ErrInvalidRegex},
		{"chain of factored three-way groups", "^" + strings.Repeat(`(?:a|ab|abc)`, 16) + "$", regexanchor.ErrInvalidRegex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, fmt.Sprintf(`name.matches("%s")`, tc.pattern))
			for label, opts := range map[string][]filter.PolicyOption{"trusted": trusted, "untrusted": untrusted} {
				for backend, err := range translateEverywhere(t, node, opts) {
					if tc.want == nil {
						require.NoError(t, err, "%s %s", label, backend)
						continue
					}

					require.ErrorIs(t, err, tc.want, "%s %s", label, backend)
				}
			}
		})
	}
}

// TestSafeRegex_UnsafeOptOut pins [filter.WithUnsafeRegex]: it lifts the safe subset for patterns a service writes itself, and it
// cannot be combined with [filter.WithUntrustedInput].
func TestSafeRegex_UnsafeOptOut(t *testing.T) {
	t.Parallel()

	opts := []filter.PolicyOption{filter.WithAllowedFields("name"), filter.WithUnsafeRegex(true)}

	for _, pattern := range []string{`(a|aa)+$`, `.*.*.*x`, `x+y+z+`} {
		node := filtertest.MustParseFilter(t, fmt.Sprintf(`name.matches("%s")`, pattern))
		for backend, err := range translateEverywhere(t, node, opts) {
			require.NoError(t, err, "%s %s", pattern, backend)
		}
	}

	untrusted := []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("name"), filter.WithSafeRegex(true)}
	_, err := filter.NewPolicy(append(slices.Clip(untrusted), filter.WithUnsafeRegex(true))...)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

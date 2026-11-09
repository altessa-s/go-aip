// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// FuzzParse parses arbitrary input with a caching and an uncaching parser and validates the result against a policy whose
// allow-list is a wildcard prefix ("a.*"). Neither may panic, both must agree on whether the input parses, every field of
// a parsed tree must be a dotted path of plain identifiers, and a tree the policy accepts may only name fields under "a."
// with no empty segment — the guarantees SEC-1 rests on.
func FuzzParse(f *testing.F) {
	f.Add(`name == "John"`)
	f.Add(`age > 18 && active == true`)
	f.Add(`status in ["a", "b"]`)
	f.Add(`name.contains("oh")`)
	f.Add(``)
	f.Add(`((((`)
	f.Add(`== ==`)
	f.Add(`a.b == 1`)
	f.Add("a.`b c` == 1")
	f.Add("a.`zz NOT EXISTS AND secret` == \"guess\"")
	f.Add("a.`x` == 1 && a.`y NOT EXISTS OR secret` != 0")
	f.Add("a.`.b` == 1")
	f.Add("a.`b.` == 1")
	f.Add("a.`b.c` == 1")
	f.Add("a.`b/c-d` == 1")
	f.Add("has(a.`b c`)")
	f.Add("size(a.`x-y`) > 1")
	f.Add(`.a.b == 1`)
	f.Add("a.`1b` == 1")

	uncached, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(f, err)
	cached, err := filterparser.New(filterparser.WithCacheSize(64), filterparser.WithCacheBytes(1<<20))
	require.NoError(f, err)
	policy, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("a.*")))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, input string) {
		node, err := uncached.Parse(t.Context(), input)

		for range 2 { // the second parse may be a cache hit, of the tree or of the failure
			_, cachedErr := cached.Parse(t.Context(), input)
			require.Equal(t, err == nil, cachedErr == nil, "cached and uncached parsers disagree on %q", input)
		}

		if err != nil {
			return
		}

		for n := range filter.AllNodes(node) {
			if ident, ok := n.(*filter.IdentNode); ok {
				require.True(t, filter.IsFieldPath(ident.Name), "parsed field %q", ident.Name)
			}
		}

		if filter.Validate(node, policy) != nil {
			return
		}

		for n := range filter.AllNodes(node) {
			if ident, ok := n.(*filter.IdentNode); ok {
				require.True(t, strings.HasPrefix(ident.Name, "a.") && filter.IsFieldPath(ident.Name), "accepted field %q", ident.Name)
			}
		}
	})
}

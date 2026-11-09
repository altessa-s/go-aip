// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// MustParseFilter creates a no-cache [filterparser.Parser] and parses the given
// expression, failing the test on any error. Accepts [testing.TB] so it can
// be called from both tests and benchmarks.
func MustParseFilter(tb testing.TB, expr string) filter.Node {
	tb.Helper()

	p, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(tb, err)
	node, err := p.Parse(tb.Context(), expr)
	require.NoError(tb, err, "Parse(%q)", expr)

	return node
}

// MustPolicy builds a [filter.Policy] from opts, failing the test on any
// error. Translator and evaluator tests pass it to their constructors.
func MustPolicy(tb testing.TB, opts ...filter.PolicyOption) *filter.Policy {
	tb.Helper()

	policy, err := filter.NewPolicy(opts...)
	require.NoError(tb, err)

	return policy
}

// AnyFieldPolicy builds the policy of a test translator: filter.WithAnyField first (these tests pin translation itself, not the
// allow-list, so a test without WithAllowedFields translates any field; an allow-list given in opts still applies), then
// every filter.PolicyOption of opts. The filter.TranslatorOption values of opts bind the policy to storage names.
func AnyFieldPolicy(tb testing.TB, opts ...any) (*filter.Policy, []filter.TranslatorOption) {
	tb.Helper()

	rules := []filter.PolicyOption{filter.WithAnyField(true)}

	var binding []filter.TranslatorOption

	for _, opt := range opts {
		switch opt := opt.(type) {
		case filter.PolicyOption:
			rules = append(rules, opt)
		case filter.TranslatorOption:
			binding = append(binding, opt)
		default:
			tb.Fatalf("unexpected test translator option %T", opt)
		}
	}

	return MustPolicy(tb, rules...), binding
}

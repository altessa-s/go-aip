// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"errors"
	"regexp/syntax"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

func TestRegexDependenciesAndIsolation(t *testing.T) {
	t.Parallel()

	calls := 0
	p, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithRegexCacheSize(0), filter.WithRegexValidator(func(string) error { return nil }), filter.WithRegexCompiler(func(s string) (filter.RegexMatcher, error) { calls++; return literalMatcher(s), nil }))
	require.NoError(t, err)
	r, err := p.CompileRegex("a")
	require.NoError(t, err)
	require.True(t, r.MatchString("a"))
	require.False(t, r.MatchString("b"))
	require.Equal(t, 1, calls)

	first, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	second, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	_, err = first.CompileRegex("x")
	require.NoError(t, err)

	_, misses, _ := second.RegexCacheStats()
	require.Zero(t, misses)
}

type literalMatcher string

func (m literalMatcher) MatchString(s string) bool { return string(m) == s }

func TestRegexErrorCauses(t *testing.T) {
	t.Parallel()

	cause := &syntax.Error{Code: syntax.ErrMissingBracket, Expr: "["}

	for _, option := range []filter.PolicyOption{
		filter.WithRegexValidator(func(string) error { return cause }),
		filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { return nil, cause }),
	} {
		policy, err := filter.NewPolicy(filter.WithAnyField(true), option)
		require.NoError(t, err)
		_, err = policy.CompileRegex("valid")
		require.ErrorIs(t, err, filter.ErrInvalidRegex)
		require.ErrorIs(t, err, cause)
		typed, ok := errors.AsType[*syntax.Error](err)
		require.True(t, ok)
		require.Same(t, cause, typed)
	}

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	_, err = policy.CompileRegex("[")
	require.ErrorIs(t, err, filter.ErrInvalidRegex)
	_, ok := errors.AsType[*syntax.Error](err)
	require.True(t, ok)
}

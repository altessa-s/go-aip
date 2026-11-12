// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestUnknownOperandsAllocateNothing evaluates prepared filters whose null operands are unknown on every record and absorbed by
// || or &&: the unknowns are shared values, so a record costs no more allocations than one with known operands.
//
// Serial: testing.AllocsPerRun refuses to run in a parallel test.
func TestUnknownOperandsAllocateNothing(t *testing.T) {
	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("age", "name", "note", "flag")))
	require.NoError(t, err)

	tests := []struct {
		expr string
		want bool
	}{
		{`age > 5 || name == "y"`, true},
		{`age > 5 && name == "x"`, false},
		{`name.contains(note) || name == "y"`, true},
		{`note.substring(1) == "x" || name == "y"`, true},
		{`name.substring(note) == "x" || name == "y"`, true},
		{`name.matches(note) || name == "y"`, true},
		{`size(note) > 1 || name == "y"`, true},
		{`!flag || name == "y"`, true},
		{`(flag || age < 1) || name == "y"`, true},
		{`flag && name == "y"`, false},
		{`age > 5`, false},
	}

	data := map[string]any{"age": nil, "name": "y", "note": nil, "flag": nil}

	for _, tc := range tests {
		prepared, err := eval.Prepare(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)

		got, err := prepared.Evaluate(data)
		require.NoError(t, err, tc.expr)
		require.Equal(t, tc.want, got, tc.expr)

		if raceEnabled {
			continue
		}

		allocs := testing.AllocsPerRun(20, func() { _, _ = prepared.Evaluate(data) })
		require.Zero(t, allocs, tc.expr)
	}
}

// TestPreparedMatchesLiteralPatterns evaluates prepared matches() calls with literal and field patterns: the literal patterns
// compiled by Prepare match as the shared cache's do, a field pattern is still compiled per record, and a pattern the policy
// rejects fails at evaluation as before.
func TestPreparedMatchesLiteralPatterns(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("email", "pattern")))
	require.NoError(t, err)

	tests := []struct {
		expr string
		data map[string]any
		want bool
	}{
		{`email.matches("^[a-z]+@example\\.com$")`, map[string]any{"email": "alice@example.com"}, true},
		{`email.matches("^[a-z]+@example\\.com$")`, map[string]any{"email": "Alice@example.com"}, false},
		{`email.matches("^a") && !email.matches("^a") || email.matches("com$")`, map[string]any{"email": "alice@example.com"}, true},
		{`email.matches(pattern)`, map[string]any{"email": "alice@example.com", "pattern": "^alice@"}, true},
		{`email.matches(pattern)`, map[string]any{"email": "alice@example.com", "pattern": "^bob@"}, false},
	}

	for _, tc := range tests {
		prepared, err := eval.Prepare(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)

		for range 2 {
			got, err := prepared.Evaluate(tc.data)
			require.NoError(t, err, tc.expr)
			require.Equal(t, tc.want, got, tc.expr)
		}
	}

	prepared, err := eval.Prepare(filtertest.MustParseFilter(t, `email.matches(pattern)`))
	require.NoError(t, err)

	_, err = prepared.Evaluate(map[string]any{"email": "a", "pattern": "("})
	require.ErrorIs(t, err, filter.ErrInvalidRegex)
}

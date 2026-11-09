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

// truth is a three-valued truth value of SQL's logic.
type truth int

const (
	tFalse truth = iota
	tTrue
	tUnknown
)

func (a truth) not() truth {
	switch a {
	case tTrue:
		return tFalse
	case tFalse:
		return tTrue
	default:
		return tUnknown
	}
}

func (a truth) and(b truth) truth {
	switch {
	case a == tFalse || b == tFalse:
		return tFalse
	case a == tUnknown || b == tUnknown:
		return tUnknown
	default:
		return tTrue
	}
}

func (a truth) or(b truth) truth { return a.not().and(b.not()).not() }

// kleeneLeaf is a predicate with its truth value on [kleeneRow].
type kleeneLeaf struct {
	expr  string
	value truth
}

// kleeneRow holds the values the leaves of [kleeneLeaves] read: x, s, b, n and arg are absent or null.
func kleeneRow() map[string]any {
	return map[string]any{"t": true, "f": false, "name": "abc", "b": nil, "arg": nil, "tags": []any{"a"}}
}

// kleeneLeaves are a true and a false predicate and every data-driven unknown: an ordering comparison with an absent or null
// operand, size() and substring() of an absent value, a substring() out of range, a null string-function argument and substring()
// bound, a bare null bool.
var kleeneLeaves = []kleeneLeaf{
	{`t`, tTrue},
	{`f`, tFalse},
	{`x > 1`, tUnknown},
	{`1 <= x`, tUnknown},
	{`name < b`, tUnknown},
	{`size(s) > 0`, tUnknown},
	{`s.substring(0) == "a"`, tUnknown},
	{`name.substring(n) == "a"`, tUnknown},
	{`name.substring(1, 9) == "a"`, tUnknown},
	{`name.contains(arg)`, tUnknown},
	{`name.matches(arg)`, tUnknown},
	{`b`, tUnknown},
	{`tags > b`, tUnknown},
	// Not unknown: equality with null, a string function on a null target, membership, has() and size() of an array.
	{`x == 1`, tFalse},
	{`x != 1`, tTrue},
	{`s.contains("a")`, tFalse},
	{`x in [1, 2]`, tFalse},
	{`has(o.x)`, tFalse},
	{`size(missing) == 0`, tTrue},
}

// TestKleeneLogic checks that the default evaluator applies SQL's three-valued logic to the data-driven unknowns: on every
// combination of two leaves under &&, || and !, in either order, it matches exactly where SQL's WHERE would (true), and an
// unknown filter does not match without an error.
func TestKleeneLogic(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags", "missing"))
	e, err := evaluator.New(policy)
	require.NoError(t, err)

	type composed struct {
		expr  string
		value truth
	}

	var exprs []composed

	for _, l := range kleeneLeaves {
		exprs = append(exprs, composed{l.expr, l.value}, composed{"!(" + l.expr + ")", l.value.not()},
			composed{"!(!(" + l.expr + "))", l.value})

		for _, r := range kleeneLeaves {
			and, or := l.value.and(r.value), l.value.or(r.value)
			exprs = append(exprs,
				composed{l.expr + " && " + r.expr, and}, composed{l.expr + " || " + r.expr, or},
				composed{"!(" + l.expr + " && " + r.expr + ")", and.not()}, composed{"!(" + l.expr + " || " + r.expr + ")", or.not()},
				composed{"!(" + l.expr + ") || " + r.expr, l.value.not().or(r.value)},
				composed{"(" + l.expr + " || " + r.expr + ") && !(" + r.expr + ")", or.and(r.value.not())},
			)
		}
	}

	row := kleeneRow()

	for _, c := range exprs {
		node := filtertest.MustParseFilter(t, c.expr)

		got, err := e.Evaluate(node, row)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.value == tTrue, got, c.expr)

		prepared, err := e.Prepare(node)
		require.NoError(t, err, c.expr)
		got, err = prepared.Evaluate(row)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.value == tTrue, got, c.expr)
	}
}

// TestKleeneKeepsErrors checks that only data-driven unknowns are absorbed: a value of an incomparable or unsupported type, an
// exhausted data budget and an operation limit still fail the evaluation, whatever the other operand.
func TestKleeneKeepsErrors(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	e, err := evaluator.New(policy)
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		row  map[string]any
		err  error
	}{
		{`n > 1 || t`, map[string]any{"n": "abc", "t": true}, filter.ErrUnsupportedType},
		{`x > 1 || n > 1`, map[string]any{"n": "abc"}, filter.ErrUnsupportedType},
		{`n > 1 && x > 1`, map[string]any{"n": "abc"}, filter.ErrUnsupportedType},
		{`!(n > 1)`, map[string]any{"n": "abc"}, filter.ErrUnsupportedType},
		{`x > 1 || s`, map[string]any{"s": "yes"}, filter.ErrInvalidExpression},
		{`name.contains(arg) || t`, map[string]any{"name": "a", "arg": int64(1), "t": true}, filter.ErrInvalidExpression},
		{`size(n) > 0 || t`, map[string]any{"n": int64(1), "t": true}, filter.ErrUnsupportedOperation},
	} {
		_, err := e.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.row)
		require.ErrorIs(t, err, tc.err, tc.expr)
	}

	budgeted, err := evaluator.New(policy, evaluator.WithMaxDataBudget(1))
	require.NoError(t, err)
	_, err = budgeted.Evaluate(filtertest.MustParseFilter(t, `x > 1 || name == "abcdef"`), map[string]any{"name": "abcdef"})
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)

	limited, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithMaxOperations(4)))
	require.NoError(t, err)
	_, err = limited.Evaluate(filtertest.MustParseFilter(t, `x > 1 || t == true`), map[string]any{"t": true})
	require.ErrorIs(t, err, filter.ErrMaxOperationsExceeded)
}

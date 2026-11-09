// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

type mapReader struct{}

func (mapReader) Lookup(data map[string]any, name string) (any, bool, error) {
	v, ok := data[name]
	return v, ok, nil
}

func TestProfileWithCustomReader(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	p, err := evaluation.New(s, mapReader{})
	require.NoError(t, err)
	ev, err := evaluator.New(s.FilterPolicy(), evaluator.WithProfile(p))
	require.NoError(t, err)
	q, err := s.Compile(t.Context(), listing.Request{Filter: `id == "a"`})
	require.NoError(t, err)
	prepared, err := ev.Prepare(q.Filter)
	require.NoError(t, err)

	q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode).Value = "b"
	got, err := prepared.Evaluate(map[string]any{"id": "a"})
	require.NoError(t, err)
	require.True(t, got)
}

func TestReaderBudget(t *testing.T) {
	t.Parallel()

	r := evaluation.NewReader(nil, 1)
	_, _, err := r.Lookup(map[string]any{"nested": map[string]any{"id": "a"}}, "nested.id")
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

type countingBudget struct{ used int }

func (b *countingBudget) Charge(n int) bool { b.used += n; return true }

func TestProfileSharesPredicateAndReadBudget(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 100))
	require.NoError(t, err)

	budget := &countingBudget{}
	ev, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile.BindBudget(budget)))
	require.NoError(t, err)
	q, err := schema.Compile(t.Context(), listing.Request{Filter: `id == "a"`})
	require.NoError(t, err)
	prepared, err := ev.Prepare(q.Filter)
	require.NoError(t, err)
	_, err = prepared.Evaluate(map[string]any{"id": "a"})
	require.NoError(t, err)

	first := budget.used
	require.GreaterOrEqual(t, first, 3) // predicate plus map and scalar reads

	_, err = prepared.Evaluate(map[string]any{"id": "a"})
	require.NoError(t, err)
	require.Equal(t, first*2, budget.used)
	// Binding leaves the reusable profile unmodified.
	plain, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile))
	require.NoError(t, err)
	_, err = plain.Evaluate(q.Filter, map[string]any{"id": "a"})
	require.NoError(t, err)
	require.Equal(t, first*2, budget.used)
}

type budgetedMapReader struct{ budget evaluator.Budget }

func (r budgetedMapReader) BindBudget(b evaluator.Budget) evaluation.FieldReader {
	return budgetedMapReader{budget: b}
}

func (r budgetedMapReader) Lookup(data map[string]any, name string) (any, bool, error) {
	if r.budget != nil && !r.budget.Charge(7) {
		return nil, false, filter.ErrDataLimitExceeded
	}

	v, ok := data[name]

	return v, ok, nil
}

func TestCustomReaderBindsSharedBudget(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	profile, err := evaluation.New(schema, budgetedMapReader{})
	require.NoError(t, err)

	budget := &countingBudget{}
	ev, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile.BindBudget(budget)))
	require.NoError(t, err)
	q, err := schema.Compile(t.Context(), listing.Request{Filter: `id == "a"`})
	require.NoError(t, err)
	matched, err := ev.Evaluate(q.Filter, map[string]any{"id": "a"})
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, 8, budget.used)
}

func TestProfileTimestampPrecision(t *testing.T) {
	t.Parallel()

	for _, precision := range []time.Duration{time.Millisecond, time.Microsecond, 0} {
		t.Run(precision.String(), func(t *testing.T) {
			t.Parallel()

			s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"id"}, listing.NewProfile(listing.WithTimestampPrecision(precision)))
			require.NoError(t, err)
			p, err := evaluation.New(s, mapReader{})
			require.NoError(t, err)
			ev, err := evaluator.New(s.FilterPolicy(), evaluator.WithProfile(p))
			require.NoError(t, err)

			at := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
			for _, op := range []string{"==", ">", "in"} {
				expression := fmt.Sprintf("at %s timestamp(%q)", op, at.Format(time.RFC3339Nano))
				if op == "in" {
					expression = fmt.Sprintf("at in [timestamp(%q)]", at.Format(time.RFC3339Nano))
				}

				q, err := s.Compile(t.Context(), listing.Request{Filter: expression})
				require.NoError(t, err)
				got, err := ev.Evaluate(q.Filter, map[string]any{"at": at.Add(time.Microsecond)})
				require.NoError(t, err)

				want := precision == time.Millisecond
				if op == ">" {
					want = !want
				}

				require.Equal(t, want, got, expression)
			}
		})
	}
}

func TestProfileCustomRegexPreparedOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithBuiltinFunctions("matches"), listing.WithFilterOptions(filter.WithRegexCompiler(func(pattern string) (filter.RegexMatcher, error) {
		calls++
		return regexp.Compile("(?i)" + pattern)
	}))))
	require.NoError(t, err)
	p, err := evaluation.New(s, mapReader{})
	require.NoError(t, err)
	ev, err := evaluator.New(s.FilterPolicy(), evaluator.WithProfile(p))
	require.NoError(t, err)
	q, err := s.Compile(t.Context(), listing.Request{Filter: `id.matches("ABC")`})
	require.NoError(t, err)
	prepared, err := ev.Prepare(q.Filter)
	require.NoError(t, err)

	before := calls

	for range 2 {
		got, err := prepared.Evaluate(map[string]any{"id": "abc"})
		require.NoError(t, err)
		require.True(t, got)
	}

	require.Equal(t, before, calls)
}

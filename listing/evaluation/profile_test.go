// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"fmt"
	"regexp"
	"strings"
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

type rejectingBudget struct{ units int }

func (b *rejectingBudget) Charge(units int) bool {
	b.units += units
	return false
}

func TestFoldedContainsBudgetAndConcurrentUse(t *testing.T) {
	t.Parallel()

	s := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"},
		listing.NewProfile(listing.WithCaseInsensitiveContainsFields("id")))
	q, err := s.Compile(t.Context(), listing.Request{Filter: `id.contains("иВе")`})
	require.NoError(t, err)
	p, err := evaluation.New(s, mapReader{})
	require.NoError(t, err)

	budget := &rejectingBudget{}
	ev, err := evaluator.New(s.FilterPolicy(), evaluator.WithProfile(p.BindBudget(budget)))
	require.NoError(t, err)
	_, err = ev.Prepare(q.Filter)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
	require.Equal(t, len("иВе"), budget.units)

	program, err := p.CompileEvaluator(s.FilterPolicy(), q.Filter)
	require.NoError(t, err)

	for _, tc := range []struct {
		name, value string
		want        bool
		err         error
	}{
		{"match", "ПрИвЕт", true, nil}, {"mismatch", "ёж", false, nil},
		{"budget", strings.Repeat("ПрИвЕт", 1000), false, filter.ErrDataLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := program.Evaluate(map[string]any{"id": tc.value}, 100, nil)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPrepareContainsLiteralBudget(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithCaseInsensitiveContainsFields("id")))

	for _, tc := range []struct {
		name   string
		reader evaluation.FieldReader
		size   int
		want   error
	}{
		{"two needles fit", evaluation.NewReader(nil, 16), 8, nil},
		{"two needles exceed reader budget", evaluation.NewReader(nil, 15), 8, filter.ErrDataLimitExceeded},
		{"custom reader default", mapReader{}, evaluator.DefaultMaxDataBudget/2 + 1, filter.ErrDataLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := evaluation.New(schema, tc.reader)
			require.NoError(t, err)

			needle := &filter.LiteralNode{Value: strings.Repeat("B", tc.size)}
			call := func() filter.Node {
				return &filter.CallNode{Target: &filter.IdentNode{Name: "id"}, Op: filter.OpContains, Args: []filter.Node{needle}}
			}
			_, err = p.CompileEvaluator(schema.FilterPolicy(), &filter.BinaryOpNode{Left: call(), Op: filter.OpAnd, Right: call()})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

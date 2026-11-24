// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestRequestBudgets(t *testing.T) {
	t.Parallel()

	for name, opt := range map[string]ListEngineOption{"scan": WithListMaxScanned(2), "materialize": WithListMaxMaterialized(2), "work": WithListMaxWork(2)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, err := NewListEngine(nil, listingtest.NewSchema(t), listingtest.ID, listingtest.Values, opt)
			require.NoError(t, err)
			_, err = l.List(t.Context(), "widgets", listing.Request{}, listingtest.MakeWidgets(10))
			require.ErrorIs(t, err, ErrWorkBudget)
		})
	}
}

func TestCancelWhileScanning(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	l, err := NewListEngine(nil, listingtest.NewSchema(t), listingtest.ID, func(w listingtest.Widget) map[string]any { cancel(); return listingtest.Values(w) })
	require.NoError(t, err)
	_, err = l.List(ctx, "widgets", listing.Request{}, listingtest.MakeWidgets(10))
	require.ErrorIs(t, err, context.Canceled)
}

func TestCancelWhileSorting(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	work := workBudget{ctx: ctx}
	top := topRows[int]{compare: func(a, b pagetoken.Position) int { cancel(); return 1 }, work: &work, limit: 3}
	require.NoError(t, top.offer(1, &pagetoken.Position{}))
	require.NoError(t, top.offer(2, &pagetoken.Position{})) // the comparison cancels
	require.ErrorIs(t, top.offer(3, &pagetoken.Position{}), context.Canceled)
	_, err := top.sorted()
	require.ErrorIs(t, err, context.Canceled)
}

// TestTopRowsKeepsSmallestInOrder checks the bounded heap keeps the limit smallest positions offered, sorted ascending, keeps
// the first of equal positions, charges its comparisons and honors the materialization bound.
func TestTopRowsKeepsSmallestInOrder(t *testing.T) {
	t.Parallel()

	compare := pagetoken.ComparePositions(nil, false, pagetoken.ScalarPositionCodec{})
	at := func(n int64) *pagetoken.Position {
		id, err := pagetoken.NewSortValue(n)
		require.NoError(t, err)

		return &pagetoken.Position{ID: id}
	}

	for name, tc := range map[string]struct {
		in    []int64
		limit int
		want  []int
	}{
		"empty":       {limit: 3},
		"fewer":       {in: []int64{5, 1}, limit: 3, want: []int{1, 0}},
		"bounded":     {in: []int64{9, 3, 7, 1, 8, 2, 6}, limit: 3, want: []int{3, 5, 1}},
		"descending":  {in: []int64{6, 5, 4, 3, 2, 1}, limit: 2, want: []int{5, 4}},
		"duplicate":   {in: []int64{2, 1, 1}, limit: 1, want: []int{1}},
		"single":      {in: []int64{4, 2, 3}, limit: 1, want: []int{1}},
		"allRetained": {in: []int64{3, 1, 2}, limit: 10, want: []int{1, 2, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			work := workBudget{ctx: t.Context(), max: 1000}
			top := topRows[int]{compare: compare, work: &work, limit: tc.limit}

			for i, n := range tc.in {
				require.NoError(t, top.offer(i, at(n)))
			}

			rows, err := top.sorted()
			require.NoError(t, err)

			got := make([]int, len(rows))
			for i, r := range rows {
				got[i] = r.item
			}

			require.Equal(t, len(tc.want), len(got))

			if len(tc.want) > 0 {
				require.Equal(t, tc.want, got)
			}

			if len(tc.in) > 1 {
				require.Positive(t, work.used)
			}
		})
	}

	work := workBudget{ctx: t.Context()}
	top := topRows[int]{compare: compare, work: &work, limit: 5, maxMaterialized: 2}
	require.NoError(t, top.offer(0, at(1)))
	require.NoError(t, top.offer(1, at(2)))
	require.ErrorIs(t, top.offer(2, at(3)), ErrWorkBudget)
}

func TestEvaluationBudgetAccumulatesAcrossRows(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v} }, WithListMaxWork(10))
	require.NoError(t, err)
	// No rows match, so neither materialization nor sorting can exhaust the budget.
	_, err = list.List(t.Context(), "items", listing.Request{Filter: `id == "missing"`}, []string{"a", "b", "c"})
	require.ErrorIs(t, err, ErrWorkBudget)
	// A later request gets a fresh budget.
	_, err = list.List(t.Context(), "items", listing.Request{Filter: `id == "missing"`}, []string{"a"})
	require.NoError(t, err)
}

func TestFieldReadsShareRequestBudget(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "nested.id", Kind: filter.FieldKindString}}, []string{"nested.id"}, nil)
	require.NoError(t, err)
	list, err := NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"nested": map[string]any{"id": v}} }, WithListMaxWork(4))
	require.NoError(t, err)
	// One scan fits, but reading the nested sort key exhausts the shared budget.
	_, err = list.List(t.Context(), "items", listing.Request{}, []string{"a"})
	require.ErrorIs(t, err, ErrWorkBudget)
}

type requestProfile struct{ *evaluation.Profile }

func (p requestProfile) BindBudget(b evaluator.Budget) evaluator.Profile {
	b.Charge(100)
	return p.Profile
}

func TestCustomProfileChargesRequestBudget(t *testing.T) {
	t.Parallel()
	schema := listingtest.NewSchema(t)
	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 100))
	require.NoError(t, err)
	list, err := NewListEngine(nil, schema, listingtest.ID, listingtest.Values, WithListProfile(requestProfile{profile}), WithListMaxWork(10))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "widgets", listing.Request{}, nil)
	require.ErrorIs(t, err, ErrWorkBudget)
	_, err = NewListEngine(nil, schema, listingtest.ID, listingtest.Values, WithListProfile(struct{ evaluator.Profile }{profile}), WithListMaxWork(10))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestStringPredicateBudget(t *testing.T) {
	t.Parallel()

	for _, function := range []string{"contains", "startsWith", "endsWith", "matches"} {
		t.Run(function, func(t *testing.T) {
			t.Parallel()

			schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithBuiltinFunctions(function)))
			require.NoError(t, err)
			list, err := NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v} }, WithListMaxWork(100))
			require.NoError(t, err)
			_, err = list.List(t.Context(), "items", listing.Request{Filter: `id.` + function + `("missing")`}, []string{strings.Repeat("a", 1000)})
			require.ErrorIs(t, err, ErrWorkBudget)
		})
	}
}

type failingRequestProfile struct{ *evaluation.Profile }

func (p failingRequestProfile) BindBudget(b evaluator.Budget) evaluator.Profile {
	b.Charge(100)
	return p
}

func (p failingRequestProfile) CompileEvaluator(*filter.Policy, filter.Node) (evaluator.Program, error) {
	return nil, filter.ErrUnsupportedOperation
}

func TestPrepareErrorAfterBudgetExhausted(t *testing.T) {
	t.Parallel()
	base := listingtest.NewSchema(t)
	p, err := evaluation.New(base, evaluation.NewReader(nil, 0))
	require.NoError(t, err)
	custom, err := NewListEngine(nil, base, listingtest.ID, listingtest.Values, WithListProfile(failingRequestProfile{p}), WithListMaxWork(1))
	require.NoError(t, err)
	_, err = custom.List(t.Context(), "widgets", listing.Request{Filter: `name.contains("ABC")`}, nil)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)

	folding := listingtest.NewSchema(t, listing.WithCaseInsensitiveContainsFields("name"))
	engine, err := NewListEngine(nil, folding, listingtest.ID, listingtest.Values, WithListMaxWork(1))
	require.NoError(t, err)
	_, err = engine.List(t.Context(), "widgets", listing.Request{Filter: `name.contains("ABC")`}, nil)
	require.ErrorIs(t, err, ErrWorkBudget)
}

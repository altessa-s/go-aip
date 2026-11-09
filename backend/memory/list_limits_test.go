// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// secretID is the id of a stored item that must never appear in an error.
const secretID = "secret-record-7f3a"

type scoredItem struct {
	ID    string
	Score any
}

// TestListErrorsOmitStoredIDs checks a stored item that fails filtering or sorting is reported without its id.
func TestListErrorsOmitStoredIDs(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "score", Kind: filter.FieldKindFloat},
	}, []string{"score", "id"}, nil)
	require.NoError(t, err)

	list, err := memory.NewListEngine(nil, schema, func(it scoredItem) string { return it.ID },
		func(it scoredItem) map[string]any { return map[string]any{"id": it.ID, "score": it.Score} })
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		req     listing.Request
		item    scoredItem
		wantErr error
	}{
		"filter":          {listing.Request{Filter: `score > 1`}, scoredItem{ID: secretID, Score: math.Inf(1)}, evaluation.ErrNonFiniteFloat},
		"sort non-finite": {listing.Request{OrderBy: "score"}, scoredItem{ID: secretID, Score: math.Inf(1)}, pagetoken.ErrUnsortableValue},
		"sort class":      {listing.Request{OrderBy: "score"}, scoredItem{ID: secretID, Score: "high"}, pagetoken.ErrUnsortableValue},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := list.List(t.Context(), listingtest.Subject, tc.req, []scoredItem{tc.item})
			require.ErrorIs(t, err, tc.wantErr)
			require.Empty(t, listingtest.FieldOf(err), "a bad stored item is an internal error")
			require.NotContains(t, err.Error(), secretID)
		})
	}
}

// TestListWorkLimitIsOnByDefault checks a list without WithListMaxWork is bounded by DefaultMaxWork and that
// WithListMaxWork(0) lifts the bound.
func TestListWorkLimitIsOnByDefault(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"},
		listing.NewProfile(listing.WithBuiltinFunctions("contains")))
	require.NoError(t, err)

	// Each item costs about 1 MiB of compared text, below the per-evaluation data budget; together they exceed the request
	// budget.
	text := strings.Repeat("a", 1<<20)

	items := make([]string, memory.DefaultMaxWork/len(text)+2)
	for i := range items {
		items[i] = text
	}

	id := func(v string) string { return v }
	values := func(v string) map[string]any { return map[string]any{"id": v} }
	req := listing.Request{Filter: `id.contains("b")`}

	for name, tc := range map[string]struct {
		opts []memory.ListEngineOption
		want error
	}{
		"default":   {want: memory.ErrWorkBudget},
		"unlimited": {opts: []memory.ListEngineOption{memory.WithListMaxWork(0)}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			list, err := memory.NewListEngine(nil, schema, id, values, tc.opts...)
			require.NoError(t, err)

			_, err = list.List(t.Context(), listingtest.Subject, req, items)
			if tc.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.want)
			require.ErrorIs(t, err, listing.ErrWorkBudget)
		})
	}
}

// unboundProfile evaluates through the portable profile but does not implement memory.RequestProfile.
type unboundProfile struct{ profile *evaluation.Profile }

func (p unboundProfile) ValidatePolicy(policy *filter.Policy) error {
	return p.profile.ValidatePolicy(policy)
}

func (p unboundProfile) CompileEvaluator(policy *filter.Policy, node filter.Node) (evaluator.Program, error) {
	return p.profile.CompileEvaluator(policy, node)
}

// TestListCustomProfileNeedsExplicitUnlimitedWork checks a profile that cannot be bound to the request budget is refused
// under the default work limit and accepted once the limit is lifted explicitly.
func TestListCustomProfileNeedsExplicitUnlimitedWork(t *testing.T) {
	t.Parallel()

	schema := listingtest.NewSchema(t)
	profile, err := evaluation.New(schema, evaluation.NewReader(nil, memory.DefaultMaxDataBudget))
	require.NoError(t, err)

	unbound := memory.WithListProfile(unboundProfile{profile})

	_, err = memory.NewListEngine(nil, schema, listingtest.ID, listingtest.Values, unbound)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	list, err := memory.NewListEngine(nil, schema, listingtest.ID, listingtest.Values, unbound, memory.WithListMaxWork(0))
	require.NoError(t, err)

	got, err := list.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 3}, listingtest.MakeWidgets(5))
	require.NoError(t, err)
	require.Len(t, got.Items, 3)
}

// TestListChecksPageTokenBeforeScanning checks a forged, tampered or foreign page token is rejected before any item is read.
func TestListChecksPageTokenBeforeScanning(t *testing.T) {
	t.Parallel()

	schema := listingtest.NewSchema(t)
	codec := pagetokentest.Codec(t)
	ws := listingtest.MakeWidgets(10)

	issuer, err := memory.NewListEngine(codec, schema, listingtest.ID, listingtest.Values)
	require.NoError(t, err)

	first, err := issuer.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 2, OrderBy: "name"}, ws)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)

	tampered := []byte(first.NextPageToken)
	tampered[len(tampered)/2] ^= 1

	for name, tc := range map[string]struct {
		codec pagetoken.Signer
		req   listing.Request
	}{
		"forged":          {codec, listing.Request{PageSize: 2, OrderBy: "name", PageToken: "not-a-token"}},
		"tampered":        {codec, listing.Request{PageSize: 2, OrderBy: "name", PageToken: string(tampered)}},
		"other order":     {codec, listing.Request{PageSize: 2, OrderBy: "id", PageToken: first.NextPageToken}},
		"other filter":    {codec, listing.Request{PageSize: 2, OrderBy: "name", Filter: `priority > 1`, PageToken: first.NextPageToken}},
		"other parent":    {codec, listing.Request{PageSize: 2, OrderBy: "name", Parent: "shelves/1", PageToken: first.NextPageToken}},
		"without a codec": {nil, listing.Request{PageSize: 2, OrderBy: "name", PageToken: first.NextPageToken}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var reads atomic.Int64

			list, err := memory.NewListEngine(tc.codec, schema, listingtest.ID, func(w listingtest.Widget) map[string]any {
				reads.Add(1)
				return listingtest.Values(w)
			})
			require.NoError(t, err)

			_, err = list.List(t.Context(), listingtest.Subject, tc.req, ws)
			require.Error(t, err)
			require.Equal(t, listing.FieldPageToken, listingtest.FieldOf(err))
			require.Zero(t, reads.Load(), "no item is read for a rejected page token")
		})
	}

	// The genuine token still pages on.
	next, err := issuer.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 2, OrderBy: "name", PageToken: first.NextPageToken}, ws)
	require.NoError(t, err)
	require.Len(t, next.Items, 2)
	require.NotEqual(t, first.Items, next.Items)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//nolint:mnd,goconst // Fixed sizes, counts, page sizes and field names describe the conformance scenarios.
package listingtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"

	pagetoken "github.com/altessa-s/go-aip/pagination/pagetoken"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// equality returns field == value as a server-built AST.
func equality(field string, value any) filter.Node {
	return &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: field}, Right: &filter.LiteralNode{Value: value}}
}

// testRestriction checks the server restriction of a request applies to
// every filter — blank, narrowing, trying to widen it — after the default
// equalities, on every page.
func testRestriction(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(60)
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)
	withDefaults := newList(t, fixtureSchema(t, arrays, cfg, listing.WithDefaultEqualities(map[string]any{"kind": "KIND_A"})),
		cfg.signerFactory(t), ws)

	notArchived := equality("archived", false)
	cutoff := baseTime.Add(10*time.Minute + createdFraction)
	before := &filter.BinaryOpNode{Op: filter.OpLT, Left: &filter.IdentNode{Name: "createTime"}, Right: &filter.LiteralNode{Value: cutoff}}

	tests := []struct {
		name        string
		list        Lister
		filter      string
		restriction filter.Node
		keep        func(Widget) bool
	}{
		{name: "blank filter", list: l, restriction: notArchived, keep: func(w Widget) bool { return !w.Archived }},
		{
			name: "narrowing filter", list: l, filter: `priority > 1`, restriction: notArchived,
			keep: func(w Widget) bool { return !w.Archived && w.Priority > 1 },
		},
		{
			name: "disjunction stays grouped", list: l, filter: `priority == 0 || priority == 3`, restriction: notArchived,
			keep: func(w Widget) bool { return !w.Archived && (w.Priority == 0 || w.Priority == 3) },
		},
		{
			name: "filter cannot widen", list: l, filter: `archived == true || archived == false`, restriction: notArchived,
			keep: func(w Widget) bool { return !w.Archived },
		},
		{
			name: "after the default equalities", list: withDefaults, restriction: notArchived,
			keep: func(w Widget) bool { return !w.Archived && w.Kind == "a" },
		},
		{
			name: "filter overriding a default", list: withDefaults, filter: `kind == "KIND_B"`, restriction: notArchived,
			keep: func(w Widget) bool { return !w.Archived && w.Kind == "b" },
		},
		{
			name: "timestamp at profile precision", list: l, restriction: before,
			keep: func(w Widget) bool {
				return w.Created.Truncate(listing.TimestampPrecision).Before(cutoff.Truncate(listing.TimestampPrecision))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := expectedOrder(matching(ws, tc.keep), []orderKey{{"name", false}})
			require.NotEmpty(t, want)
			require.Less(t, len(want), len(ws))
			require.Equal(t, want, Collect(t, tc.list, listing.Request{PageSize: 7, Filter: tc.filter, OrderBy: "name", Restriction: tc.restriction}))
		})
	}

	t.Run("filter contradicting the restriction", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, Collect(t, l, listing.Request{Filter: `archived == true`, Restriction: notArchived}))
	})
}

// testRestrictionTokenBinding checks a page token is bound to the
// restriction: another or no restriction rejects it, an equal one built
// anew accepts it; and a restriction the schema refuses is a server fault.
func testRestrictionTokenBinding(t *testing.T, newList Factory, arrays bool, cfg *options) {
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), MakeWidgets(30))

	first := listing.Request{PageSize: 3, Filter: `priority >= 0`, OrderBy: "name", Restriction: equality("archived", false)}
	p, err := l(t.Context(), Subject, first)
	require.NoError(t, err)
	require.NotEmpty(t, p.NextPageToken)

	unrestricted := listing.Request{PageSize: 3, Filter: first.Filter, OrderBy: first.OrderBy}
	q, err := l(t.Context(), Subject, unrestricted)
	require.NoError(t, err)
	require.NotEmpty(t, q.NextPageToken)

	tests := []struct {
		name        string
		token       string
		restriction filter.Node
		ok          bool
	}{
		{name: "equal restriction", token: p.NextPageToken, restriction: equality("archived", false), ok: true},
		{name: "restriction removed", token: p.NextPageToken},
		{name: "other restriction", token: p.NextPageToken, restriction: equality("archived", true)},
		{name: "restriction added", token: q.NextPageToken, restriction: equality("archived", false)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := unrestricted
			req.PageToken, req.Restriction = tc.token, tc.restriction

			_, err := l(t.Context(), Subject, req)
			if tc.ok {
				require.NoError(t, err)
				return
			}

			require.Equal(t, pagetoken.TokenField, FieldOf(err), "error %v", err)
		})
	}

	t.Run("refused restriction", func(t *testing.T) {
		t.Parallel()

		_, err := l(t.Context(), Subject, listing.Request{Restriction: equality("tenant", "t1")})
		require.ErrorIs(t, err, listing.ErrInvalidRestriction)

		_, isField := errors.AsType[*coreerrs.FieldError](err)
		require.False(t, isField, "a server restriction is not blamed on a request field")
	})
}

// cutoffRequest narrows both the client filter and the stable server scope.
func cutoffRequest(at time.Time) listing.Request {
	return listing.Request{
		PageSize: 3, OrderBy: "id", Filter: "priority > 0", Restriction: equality("kind", "a"),
		Cutoff: &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.IdentNode{Name: "createTime"}, Right: &filter.LiteralNode{Value: at}},
	}
}

func cutoffIDs(ws []Widget, at time.Time) []string {
	return expectedOrder(matching(ws, func(w Widget) bool {
		return w.Priority > 0 && w.Kind == "a" && w.Created.Truncate(listing.TimestampPrecision).After(at)
	}), []orderKey{{"id", false}})
}

func testCutoff(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(120)
	list := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)
	_, err := list(t.Context(), Subject, listing.Request{Cutoff: equality("unknown", true)})
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
	require.Empty(t, FieldOf(err))

	start := baseTime.Add(3 * time.Minute)
	req := cutoffRequest(start)

	first, err := list(t.Context(), Subject, req)
	if cfg.cutoffRefusal {
		require.ErrorIs(t, err, listing.ErrInvalidRestriction)
		require.Empty(t, FieldOf(err))

		return
	}

	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)

	want := cutoffIDs(ws, start)
	require.Equal(t, want[:req.PageSize], IDs(first.Items))
	require.Equal(t, want, Collect(t, list, req))

	later := start.Add(9 * time.Minute)
	req = cutoffRequest(later)
	req.PageToken = first.NextPageToken
	last := first.Items[len(first.Items)-1].ID
	remaining := cutoffIDs(ws, later)

	index, _ := slices.BinarySearch(remaining, last)
	if index < len(remaining) && remaining[index] == last {
		index++
	}

	require.NotEmpty(t, remaining[index:])
	require.Equal(t, remaining[index:], Collect(t, list, req))
}

// RunCutoffCount checks the exact pre-pagination total under two cutoff instants
// and the classification of an invalid cutoff. items must be the widgets in scope.
func RunCutoffCount[S any](tb testing.TB, items []Widget, count func(context.Context, listing.Request, S) (int64, error), scope S) {
	tb.Helper()

	for _, at := range []time.Time{baseTime, baseTime.Add(12 * time.Minute)} {
		req := cutoffRequest(at)
		req.PageToken = "ignored by Count"
		total, err := count(tb.Context(), req, scope)
		require.NoError(tb, err)
		require.Equal(tb, int64(len(cutoffIDs(items, at))), total)
	}

	_, err := count(tb.Context(), listing.Request{Cutoff: equality("unknown", true)}, scope)
	require.ErrorIs(tb, err, listing.ErrInvalidRestriction)
	require.Empty(tb, FieldOf(err))
}

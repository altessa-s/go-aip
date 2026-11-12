// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// newWidgets returns the in-memory list of widgets.
func newWidgets(tb testing.TB, s *listing.Schema, codec pagetoken.Signer) *memory.ListEngine[listingtest.Widget] {
	tb.Helper()

	l, err := memory.NewListEngine(codec, s, listingtest.ID, listingtest.Values)
	require.NoError(tb, err)

	return l
}

// lister lists items with l.
func lister[T any](l *memory.ListEngine[T], items []T) func(context.Context, string, listing.Request) (page.Page[T], error) {
	return func(ctx context.Context, subject string, req listing.Request) (page.Page[T], error) {
		return l.List(ctx, subject, req, items)
	}
}

func TestListEngineConformance(t *testing.T) {
	t.Parallel()
	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		return lister(newWidgets(t, s, codec), items)
	})
}

func TestNewRefusesMissingParts(t *testing.T) {
	t.Parallel()
	s := listingtest.NewSchema(t)
	_, err := memory.NewListEngine[listingtest.Widget](nil, nil, listingtest.ID, listingtest.Values)
	require.ErrorIs(t, err, listing.ErrInvalidSchema, "nil schema")
	_, err = memory.NewListEngine[listingtest.Widget, string](nil, s, nil, listingtest.Values)
	require.ErrorIs(t, err, listing.ErrInvalidSchema, "nil id")
	_, err = memory.NewListEngine(nil, s, listingtest.ID, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema, "nil fields")
}

// TestValidate checks Validate reports the errors of List without the items.
func TestValidate(t *testing.T) {
	t.Parallel()

	l := newWidgets(t, listingtest.NewSchema(t, listing.WithParentRequired(true)), nil)

	tests := []struct {
		req   listing.Request
		field string
	}{
		{listing.Request{Parent: "p"}, ""},
		{listing.Request{}, listing.FieldParent},
		{listing.Request{Parent: "p", PageSize: -1}, pagetoken.SizeField},
		{listing.Request{Parent: "p", OrderBy: "owner"}, listing.FieldOrderBy},
		{listing.Request{Parent: "p", Filter: `secret == 1`}, listing.FieldFilter},
		{listing.Request{Parent: "p", Filter: `name == note`}, listing.FieldFilter},
	}
	for _, tc := range tests {
		err := l.Validate(t.Context(), tc.req)
		if tc.field == "" {
			require.NoError(t, err, "Validate(%+v)", tc.req)
			continue
		}

		require.Equal(t, tc.field, listingtest.FieldOf(err), "Validate(%+v): %v", tc.req, err)
	}
}

func TestExpiredToken(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ws := listingtest.MakeWidgets(10)
	s := listingtest.NewSchema(t)
	l := newWidgets(t, s, pagetokentest.Codec(t, pagetoken.WithTTL(time.Minute), pagetoken.WithClock(func() time.Time { return now })))
	p, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 2}, ws)
	require.NoError(t, err)
	expired := newWidgets(t, s, pagetokentest.Codec(t, pagetoken.WithTTL(time.Minute), pagetoken.WithClock(func() time.Time { return now.Add(time.Hour) })))
	_, err = expired.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 2, PageToken: p.NextPageToken}, ws)
	require.Equal(t, pagetoken.TokenField, listingtest.FieldOf(err))
	require.ErrorIs(t, err, pagetoken.ErrExpiredToken)
}

// TestKeysetPosition checks a page continues after the last item's
// position, not its index: removing items of the first page neither repeats
// nor skips the rest.
func TestKeysetPosition(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(20)
	l := newWidgets(t, listingtest.NewSchema(t), pagetokentest.Codec(t))
	all := listingtest.Collect(t, lister(l, ws), listing.Request{PageSize: 50, OrderBy: "name"})
	req := listing.Request{PageSize: 5, OrderBy: "name"}
	p, err := l.List(t.Context(), listingtest.Subject, req, ws)
	require.NoError(t, err)

	removed := map[string]bool{}
	for _, w := range p.Items[:3] {
		removed[w.ID] = true
	}

	var rest []listingtest.Widget

	for _, w := range ws {
		if !removed[w.ID] {
			rest = append(rest, w)
		}
	}

	req.PageToken = p.NextPageToken
	require.Equal(t, all[5:], listingtest.Collect(t, lister(l, rest), req))
}

func TestRejectsBadItems(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(3)
	s := listingtest.NewSchema(t)
	l, err := memory.NewListEngine(nil, s, listingtest.ID, func(w listingtest.Widget) map[string]any {
		v := listingtest.Values(w)
		delete(v, "name")

		return v
	})
	require.NoError(t, err)
	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{OrderBy: "name"}, ws)
	require.Error(t, err)
	require.Empty(t, listingtest.FieldOf(err), "an item lacking its sort value is an internal error")
	require.Contains(t, err.Error(), `sort field "name"`)

	type named string

	l, err = memory.NewListEngine(nil, s, listingtest.ID, func(w listingtest.Widget) map[string]any {
		v := listingtest.Values(w)
		v["name"] = named(w.Name)

		return v
	})
	require.NoError(t, err)
	p, err := l.List(t.Context(), listingtest.Subject, listing.Request{OrderBy: "name", Filter: `name == "name-01"`}, ws)
	require.NoError(t, err)
	require.Len(t, p.Items, 1, "a named string value is normalized to a string")
}

// pagedIDs collects the ids of every page of req over items.
func pagedIDs[T any](tb testing.TB, l *memory.ListEngine[T], items []T, id func(T) string, req listing.Request) []string {
	tb.Helper()

	var got []string

	for range 100 {
		p, err := l.List(tb.Context(), listingtest.Subject, req, items)
		require.NoError(tb, err)

		for _, it := range p.Items {
			got = append(got, id(it))
		}

		if p.NextPageToken == "" {
			return got
		}

		req.PageToken = p.NextPageToken
	}

	tb.Fatal("paging did not end")

	return nil
}

func TestBytesFieldEqualityIsScalar(t *testing.T) {
	t.Parallel()

	type item struct {
		id   string
		blob []byte
	}

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "blob", Kind: filter.FieldKindBytes},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	items := []item{{id: "a", blob: []byte("ab")}, {id: "b", blob: []byte("xy")}}
	l, err := memory.NewListEngine(nil, s, func(it item) string { return it.id }, func(it item) map[string]any {
		return map[string]any{"id": it.id, "blob": it.blob}
	})
	require.NoError(t, err)
	p, err := l.List(t.Context(), "bytes.List", listing.Request{OrderBy: "id", Filter: `blob == b"ab"`}, items)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, []string{p.Items[0].id})
}

func TestMemoryNormalizesNamedTypesAndPointers(t *testing.T) {
	t.Parallel()

	type namedString string

	type namedBool bool

	type namedInt int64

	type item struct {
		id      string
		name    namedString
		active  *namedBool
		created *time.Time
		note    *namedString
		scores  []namedInt
	}

	at := time.Date(2026, 4, 3, 2, 1, 0, 1_900_000, time.UTC)
	active := namedBool(true)
	items := []item{{id: "a", name: "alpha", active: &active, created: &at, note: nil, scores: []namedInt{2}}}
	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "active", Kind: filter.FieldKindBool},
		{Name: "created", Kind: filter.FieldKindTimestamp},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "scores", Kind: filter.FieldKindInt, Array: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, s, func(it item) string { return it.id }, func(it item) map[string]any {
		return map[string]any{"id": it.id, "name": it.name, "active": it.active, "created": it.created,
			"note": (*namedString)(nil), "scores": it.scores}
	})
	require.NoError(t, err)

	for _, expr := range []string{
		`name == "alpha"`, `name.startsWith("al")`, `size(name) == 5`, `active`, `active == true`,
		`created == timestamp("2026-04-03T02:01:00.0019Z")`, `note == ""`, `scores == 2`,
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			p, err := l.List(t.Context(), "memory.List", listing.Request{OrderBy: "id", Filter: expr}, items)
			require.NoError(t, err)
			require.Len(t, p.Items, 1)
		})
	}
}

// TestSortsNumbersAcrossTypes checks the numbers of one sort field are
// ordered by value whatever their Go type, as MongoDB sorts int64, double and
// Decimal128 together, and a value of another class is refused.
func TestSortsNumbersAcrossTypes(t *testing.T) {
	t.Parallel()

	type item struct {
		id     string
		amount any
	}

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "amount", Kind: filter.FieldKindDecimal},
	}, []string{"amount"}, nil)
	require.NoError(t, err)

	itemID := func(it item) string { return it.id }
	l, err := memory.NewListEngine(pagetokentest.Codec(t), s, itemID,
		func(it item) map[string]any { return map[string]any{"id": it.id, "amount": it.amount} })
	require.NoError(t, err)

	items := []item{
		{"a", decimal.RequireFromString("2.5")},
		{"b", int64(3)},
		{"c", 1.25},
		{"d", decimal.RequireFromString("-1")},
		{"e", int64(2)},
	}
	require.Equal(t, []string{"d", "c", "e", "a", "b"}, pagedIDs(t, l, items, itemID, listing.Request{PageSize: 2, OrderBy: "amount"}))

	items = append(items, item{"f", "3"})
	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{OrderBy: "amount"}, items)
	require.Error(t, err)
	require.Empty(t, listingtest.FieldOf(err))
	require.Contains(t, err.Error(), `sort field "amount"`)
}

// TestSortsNestedTypedValues checks a dotted scalar sort field is read
// through a typed nested map and a pointer to one, as the filter reads it.
func TestSortsNestedTypedValues(t *testing.T) {
	t.Parallel()

	type item struct {
		id   string
		meta any // map[string]string or *map[string]string
	}

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "meta.name", Kind: filter.FieldKindString},
	}, []string{"meta.name"}, nil)
	require.NoError(t, err)

	itemID := func(it item) string { return it.id }
	l, err := memory.NewListEngine(pagetokentest.Codec(t), s, itemID,
		func(it item) map[string]any { return map[string]any{"id": it.id, "meta": it.meta} })
	require.NoError(t, err)

	ptr := func(name string) *map[string]string { return &map[string]string{"name": name} }
	items := []item{
		{"a", map[string]string{"name": "delta"}},
		{"b", ptr("alpha")},
		{"c", map[string]string{"name": "charlie"}},
		{"d", ptr("bravo")},
		{"e", map[string]string{"name": "zulu"}},
	}
	got := pagedIDs(t, l, items, itemID, listing.Request{PageSize: 2, OrderBy: "meta.name", Filter: `meta.name < "z"`})
	require.Equal(t, []string{"b", "d", "c", "a"}, got)
}

// TestSchemaWithoutIDField checks the tie-breaker is the id function: the
// schema need not declare an id field, and equal sort values page in id
// order.
func TestSchemaWithoutIDField(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "rank", Kind: filter.FieldKindInt}}, []string{"rank desc"}, nil)
	require.NoError(t, err)

	type item struct {
		id   string
		rank int64
	}

	itemID := func(it item) string { return it.id }
	l, err := memory.NewListEngine(pagetokentest.Codec(t), s, itemID, func(it item) map[string]any { return map[string]any{"rank": it.rank} })
	require.NoError(t, err)

	items := []item{{"b", 1}, {"a", 1}, {"c", 2}}
	require.Equal(t, []string{"c", "b", "a"}, pagedIDs(t, l, items, itemID, listing.Request{PageSize: 1}))
	require.True(t, strings.HasSuffix(s.Default().String(), "desc"))
}

// signerAdapter proves engines and the complete conformance suite depend only on Signer.
type signerAdapter struct{ pagetoken.Signer }

func TestConformanceWithInjectedSigner(t *testing.T) {
	t.Parallel()
	listingtest.Run(t, func(t *testing.T, s *listing.Schema, signer pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		list := newWidgets(t, s, signer)

		return func(ctx context.Context, subject string, req listing.Request) (page.Page[listingtest.Widget], error) {
			return list.List(ctx, subject, req, items)
		}
	}, listingtest.WithSignerFactory(func(tb testing.TB) pagetoken.Signer { return signerAdapter{pagetokentest.Codec(tb)} }))
}

func TestExplicitIdentityField(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name, id desc"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(pagetokentest.Codec(t), s, func(id string) string { return id }, func(id string) map[string]any { return map[string]any{"name": "same", "id": id} }, memory.WithListTiebreakField("id"))
	require.NoError(t, err)
	p, err := l.List(t.Context(), "list", listing.Request{PageSize: 1}, []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, p.Items)
	p, err = l.List(t.Context(), "list", listing.Request{PageSize: 1, PageToken: p.NextPageToken}, []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, p.Items)
}

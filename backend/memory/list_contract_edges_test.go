// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

type decimalItem struct {
	ID    string
	Price decimal.Decimal
}

func TestDecimalMembershipUsesExactOperands(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "price", Kind: filter.FieldKindDecimal},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := memory.NewListEngine(nil, schema, func(item decimalItem) string { return item.ID }, func(item decimalItem) map[string]any {
		return map[string]any{"id": item.ID, "price": item.Price}
	})
	require.NoError(t, err)

	items := []decimalItem{{ID: "exact", Price: decimal.RequireFromString("0.1")}}

	for _, expr := range []string{`price in [0.1]`, `price in ["0.1"]`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			page, err := list.List(t.Context(), "items.List", listing.Request{PageSize: 10, Filter: expr}, items)
			require.NoError(t, err)

			ids := make([]string, len(page.Items))
			for i := range page.Items {
				ids[i] = page.Items[i].ID
			}

			require.Equal(t, []string{"exact"}, ids)
		})
	}
}

func TestDecimalStoredStringsCompareAndMatchArrayMembership(t *testing.T) {
	t.Parallel()

	type item struct {
		ID     string
		Amount string
		Values []string
	}

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "amount", Kind: filter.FieldKindDecimal},
		{Name: "values", Kind: filter.FieldKindDecimal, Array: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, schema, func(v item) string { return v.ID }, func(v item) map[string]any {
		return map[string]any{"id": v.ID, "amount": v.Amount, "values": v.Values}
	})
	require.NoError(t, err)

	items := []item{{ID: "match", Amount: "10.50", Values: []string{"1.5", "2"}}, {ID: "miss", Amount: "8", Values: []string{"3"}}}

	for _, expr := range []string{`amount == 10.5`, `values in [1.5, 2.0]`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			page, err := l.List(t.Context(), "items.List", listing.Request{PageSize: 10, Filter: expr}, items)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, "match", page.Items[0].ID)
		})
	}
}

func TestFloatMembership(t *testing.T) {
	t.Parallel()

	type floatItem struct {
		ID    string
		Score float64
	}

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "score", Kind: filter.FieldKindFloat},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := memory.NewListEngine(nil, schema, func(item floatItem) string { return item.ID }, func(item floatItem) map[string]any {
		return map[string]any{"id": item.ID, "score": item.Score}
	})
	require.NoError(t, err)
	page, err := list.List(t.Context(), "items.List", listing.Request{PageSize: 10, Filter: `score in [0.1]`}, []floatItem{{ID: "exact", Score: 0.1}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
}

func TestNonFiniteFloatStorageReturnsError(t *testing.T) {
	t.Parallel()

	type item struct {
		ID    string
		Score float64
	}

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "score", Kind: filter.FieldKindFloat}}, []string{"score"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, schema, func(v item) string { return v.ID }, func(v item) map[string]any { return map[string]any{"id": v.ID, "score": v.Score} })
	require.NoError(t, err)
	_, err = l.List(t.Context(), "items.List", listing.Request{PageSize: 10}, []item{{ID: "bad", Score: math.Inf(1)}})
	require.ErrorIs(t, err, pagetoken.ErrUnsortableValue)
	require.Contains(t, err.Error(), "non-finite float")
}

type bytesItem struct {
	ID      string
	Payload []byte
}

func TestOptionalNilBytesResolveToEmpty(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "data.payload", Kind: filter.FieldKindBytes, Optional: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := memory.NewListEngine(nil, schema, func(item bytesItem) string { return item.ID }, func(item bytesItem) map[string]any {
		return map[string]any{"id": item.ID, "data": map[string]any{"payload": item.Payload}}
	})
	require.NoError(t, err)

	items := []bytesItem{{ID: "nil", Payload: nil}}

	for _, tc := range []struct {
		expr string
		want bool
	}{{`data.payload == b""`, true}, {`data.payload == null`, true}, {`has(data.payload)`, false}} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			page, err := list.List(t.Context(), "items.List", listing.Request{PageSize: 10, Filter: tc.expr}, items)
			require.NoError(t, err)
			require.Equal(t, tc.want, len(page.Items) == 1)
		})
	}
}

func TestRequiredNilBytesResolveToEmpty(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "payload", Kind: filter.FieldKindBytes}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, schema, func(v bytesItem) string { return v.ID }, func(v bytesItem) map[string]any { return map[string]any{"id": v.ID, "payload": v.Payload} })
	require.NoError(t, err)

	for _, tc := range []struct {
		filter string
		want   int
	}{{`payload == b""`, 1}, {`payload != b""`, 0}} {
		t.Run(tc.filter, func(t *testing.T) {
			t.Parallel()
			page, err := l.List(t.Context(), "items.List", listing.Request{PageSize: 10, Filter: tc.filter}, []bytesItem{{ID: "nil"}})
			require.NoError(t, err)
			require.Len(t, page.Items, tc.want)
		})
	}
}

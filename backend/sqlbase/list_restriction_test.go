// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"

	pagetoken "github.com/altessa-s/go-aip/pagination/pagetoken"
)

// TestListAppliesRestriction checks the request restriction reaches the
// WHERE clause, with and without a client filter, and binds the page token.
func TestListAppliesRestriction(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{
			Columns: []string{"id", "name", "id", "name"},
			Rows:    [][]driver.Value{{"w1", "alpha", "w1", "alpha"}, {"w2", "beta", "w2", "beta"}},
		}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString},
	}, []string{"name"}, listing.NewProfile(listing.WithServerOnlyFields("owner")))
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "widgets", []string{"id", "name"}, func(r *row) []any { return []any{&r.ID, &r.Name} }, postgres.ListDialect{})
	require.NoError(t, err)

	tenant := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "owner"}, Right: &filter.LiteralNode{Value: "tenant-a"}}

	first, err := l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", Restriction: tenant}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)
	require.Contains(t, fake.Calls()[0].Query, `"owner" = `)
	require.Equal(t, []any{"tenant-a"}, fake.Calls()[0].Args)

	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", Filter: `name != "x"`, Restriction: tenant}, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"x", "tenant-a"}, fake.Calls()[1].Args)

	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", PageToken: first.NextPageToken, Restriction: tenant}, nil)
	require.NoError(t, err)

	calls := len(fake.Calls())
	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", PageToken: first.NextPageToken}, nil)
	require.Equal(t, pagetoken.TokenField, listingtest.FieldOf(err), "a token of a restricted list is refused without the restriction")
	require.Len(t, fake.Calls(), calls, "no query runs for a refused token")

	_, err = l.List(t.Context(), "widgets.List", listing.Request{Filter: `owner == "tenant-a"`}, nil)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.Len(t, fake.Calls(), calls, "no query runs for a refused filter")

	_, err = l.List(t.Context(), "widgets.List", listing.Request{Restriction: &filter.BinaryOpNode{
		Op: filter.OpEqual, Left: &filter.IdentNode{Name: "tenant"}, Right: &filter.LiteralNode{Value: "t"},
	}}, nil)
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
}

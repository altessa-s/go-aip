// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/softdelete"
)

type widget struct{ ID, Name, Owner string }

func TestNewListEngine_PostgresQuery(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name", "owner"}}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString},
	}, []string{"name"}, nil)
	require.NoError(t, err)

	list, err := postgres.NewListEngine[widget](db, nil, schema, "widgets", []string{"id", "name", "owner"},
		func(w *widget) []any { return []any{&w.ID, &w.Name, &w.Owner} }, sqlbase.WithListScopeKeys("owner"), sqlbase.WithListParentKey("owner"))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "widgets.List",
		listing.Request{PageSize: 5, Parent: "tenant-a", OrderBy: "name", Filter: `name.startsWith("a")`}, sqlbase.Scope{"owner": "tenant-a"})
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Contains(t, calls[0].Query, `"owner" = $2`)
	require.Contains(t, calls[0].Query, `starts_with("name", ($1)::text)`)
	require.Contains(t, calls[0].Query, `ORDER BY "_listing"."name" ASC, "_listing"."id" ASC`)
	require.Equal(t, []any{"a", "tenant-a"}, calls[0].Args)
}

func TestNewListEngine_NilSchema(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	_, err := postgres.NewListEngine[widget](db, nil, nil, "widgets", []string{"id"}, func(w *widget) []any { return []any{&w.ID} })
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestListEngineServerOnlyTimestamps(t *testing.T) {
	t.Parallel()

	db, fake := sqlbasetest.NewFakeDB(t, nil)
	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
		{Name: "purgeTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}, []string{"id"}, listing.NewProfile(listing.WithServerOnlyFields("deleteTime", "purgeTime")))
	list := postgres.MustNewListEngine(db, nil, schema, "widgets", []string{"id"},
		func(id *string) []any { return []any{id} },
		sqlbase.WithListColumns(map[string]string{"deleteTime": "deleted_at", "purgeTime": "purge_at"}))

	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	policy := softdelete.New()
	req := listing.Request{Filter: `id != "hidden"`, Restriction: policy.Restriction(false), Cutoff: policy.Cutoff(at)}
	_, err := list.List(t.Context(), "widgets.List", req, nil)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Contains(t, calls[0].Query, `("deleted_at") IS NULL`)
	require.Contains(t, calls[0].Query, `"purge_at" >`)
	require.Equal(t, []any{"hidden", at}, calls[0].Args)
}

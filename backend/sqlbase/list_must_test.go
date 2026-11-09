// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

type mustRow struct{ ID, Name string }

func mustRowDest(r *mustRow) []any { return []any{&r.ID, &r.Name} }

func newPostgresList(q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*mustRow) []any,
) (*sqlbase.ListEngine[mustRow], error) {
	return sqlbase.NewListEngine(q, codec, schema, table, columns, dest, postgres.ListDialect{})
}

func mustPostgresList(q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*mustRow) []any,
) *sqlbase.ListEngine[mustRow] {
	return sqlbase.MustNewListEngine(q, codec, schema, table, columns, dest, postgres.ListDialect{})
}

func TestMustNewListEngineReturnsTheEngine(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)

	want, err := newPostgresList(db, nil, schema, "widgets", []string{"id", "name"}, mustRowDest)
	require.NoError(t, err)

	got := mustPostgresList(db, nil, schema, "widgets", []string{"id", "name"}, mustRowDest)
	require.NotNil(t, got)
	require.Equal(t, want.Indexes(), got.Indexes())
}

// TestMustNewListEnginePanics checks that MustNewListEngine panics with an
// error that names it and wraps the NewListEngine error.
func TestMustNewListEnginePanics(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	_, want := newPostgresList(db, nil, nil, "widgets", []string{"id"}, mustRowDest)
	require.ErrorIs(t, want, listing.ErrInvalidSchema)

	var recovered any

	func() {
		defer func() { recovered = recover() }()

		mustPostgresList(db, nil, nil, "widgets", []string{"id"}, mustRowDest)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "panic value %T is not an error", recovered)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Equal(t, "sqlbase.MustNewListEngine: "+want.Error(), err.Error())
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

type mustRow struct{ ID, Name string }

func mustRowDest(r *mustRow) []any { return []any{&r.ID, &r.Name} }

func TestMustNewListEngineReturnsTheEngine(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)

	want, err := mariadb.NewListEngine[mustRow](db, nil, schema, "widgets", []string{"id", "name"}, mustRowDest)
	require.NoError(t, err)

	got := mariadb.MustNewListEngine[mustRow](db, nil, schema, "widgets", []string{"id", "name"}, mustRowDest)
	require.NotNil(t, got)
	require.Equal(t, want.Indexes(), got.Indexes())
}

// TestMustNewListEnginePanics checks that MustNewListEngine panics with the
// sqlbase.MustNewListEngine error that wraps the NewListEngine error.
func TestMustNewListEnginePanics(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	_, want := mariadb.NewListEngine[mustRow](db, nil, nil, "widgets", []string{"id"}, mustRowDest)
	require.ErrorIs(t, want, listing.ErrInvalidSchema)

	var recovered any

	func() {
		defer func() { recovered = recover() }()

		mariadb.MustNewListEngine[mustRow](db, nil, nil, "widgets", []string{"id"}, mustRowDest)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "panic value %T is not an error", recovered)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Equal(t, "sqlbase.MustNewListEngine: "+want.Error(), err.Error())
}

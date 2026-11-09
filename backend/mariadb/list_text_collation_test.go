// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestMariaDBTextCollationPropagation(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Columns: []string{"id"}} })
	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := mariadb.NewListEngine[string](db, nil, s, "widgets", []string{"id"}, func(v *string) []any { return []any{v} }, sqlbase.WithListArrayStorage(sqlbase.ArraysJSON), sqlbase.WithListTextCollation("utf8mb4_bin"))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{Filter: `tags < "b"`}, nil)
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[0].Query, "utf8mb4_bin")
	require.NotContains(t, fake.Calls()[0].Query, "utf8mb4_nopad_bin")
}

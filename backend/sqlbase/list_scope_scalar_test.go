// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestUndeclaredScopeScalars(t *testing.T) {
	t.Parallel()

	for _, value := range []any{int(7), int32(7), uint32(7), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), decimal.RequireFromString("1.25")} {
		db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{Columns: []string{"id"}} })
		schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
		require.NoError(t, err)
		list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("tenant"))
		require.NoError(t, err)
		_, err = list.List(t.Context(), "widgets.List", listing.Request{}, sqlbase.Scope{"tenant": value})
		require.NoError(t, err)
		require.Len(t, fake.Calls(), 1)
	}
}

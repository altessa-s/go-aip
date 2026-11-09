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
)

func TestIndependentIDColumn(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{} })
	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "seq_no", Kind: filter.FieldKindString}}, []string{"id, seq_no"}, nil)
	engine, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(s *string) []any { return []any{s} }, postgres.ListDialect{}, sqlbase.WithListIDColumn("seq_no"))
	require.NoError(t, err)
	require.Contains(t, engine.Indexes()[0], `("id" ASC, "seq_no" ASC)`)
}

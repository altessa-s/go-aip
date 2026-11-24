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

func TestListCaseInsensitiveContainsRequiresDialectAndTextStorage(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"},
		listing.NewProfile(listing.WithCaseInsensitiveContainsFields("id")))

	for _, tc := range []struct {
		name    string
		dialect sqlbase.ListDialect
		opts    []sqlbase.ListEngineOption
	}{
		{"missing capability", thirdDialect{}, nil},
		{"UUID identity", postgres.ListDialect{}, []sqlbase.ListEngineOption{sqlbase.WithListIDType(sqlbase.IDUUID)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, fake := sqlbasetest.NewFakeDB(t, nil)
			_, err := sqlbase.NewListEngine(db, nil, schema, "terms", []string{"id"}, func(id *string) []any { return []any{id} }, tc.dialect, tc.opts...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			require.Empty(t, fake.Calls())
		})
	}
}

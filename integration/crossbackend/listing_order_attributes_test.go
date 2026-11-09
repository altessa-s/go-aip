// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"
)

type attributeOrderParser struct{ key orderby.Key }

func (p attributeOrderParser) Parse(context.Context, string) (orderby.Spec, error) {
	return orderby.Spec{Keys: []orderby.Key{p.key}}, nil
}

func TestOrderAttributesCanonicalAndEngineValidation(t *testing.T) {
	t.Parallel()

	plain := orderby.Spec{Keys: []orderby.Key{{Name: "id"}}}
	for _, key := range []orderby.Key{{Name: "id", Nulls: orderby.NullsLast}, {Name: "id", Collation: "binary"}} {
		spec := orderby.Spec{Keys: []orderby.Key{key}}
		require.NotEqual(t, plain.String(), spec.String())

		schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithOrderParser(attributeOrderParser{key})))
		require.NoError(t, err)
		q, err := schema.Compile(t.Context(), listing.Request{})
		require.NoError(t, err)
		require.Equal(t, spec.String(), q.Order.String())

		_, err = memory.NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v} })
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
		db, _ := sqlbasetest.NewFakeDB(t, nil)
		_, err = sqlbase.NewListEngine[string](db, nil, schema, "widgets", []string{"id"}, func(v *string) []any { return []any{v} }, postgres.ListDialect{})
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
	}
}

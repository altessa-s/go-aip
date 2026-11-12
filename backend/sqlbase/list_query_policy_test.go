// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"

	pagetoken "github.com/altessa-s/go-aip/pagination/pagetoken"
)

type disjunctiveDialect struct{ postgres.ListDialect }

func (d disjunctiveDialect) NewTranslator(*filter.Policy, *sqlbase.Schema, []filter.TranslatorOption, sqlbase.ListTranslatorConfig) (sqlbase.ListFilterTranslator, error) {
	return d, nil
}

func (disjunctiveDialect) Translate(filter.Node) (string, []any, error) {
	return `"name" = $1 OR "name" = 'a?b'`, []any{"a"}, nil
}

func TestFilterSQLIsolation(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, disjunctiveDialect{}, sqlbase.WithListScopeKeys("tenant"))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "list", listing.Request{Filter: `name == "a"`}, sqlbase.Scope{"tenant": "t"})
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[0].Query, `WHERE ("name" = $1 OR "name" = 'a?b') AND ("tenant" = $2)`)
	require.Equal(t, []any{"a", "t"}, fake.Calls()[0].Args)
}

func TestUnrestrictedOrderValidation(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "tenant", Kind: filter.FieldKindString}}, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true)))
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("tenant"))
	require.NoError(t, err)

	for _, order := range []string{"id, name", "tenant"} {
		require.NotPanics(t, func() {
			_, err := l.List(t.Context(), "list", listing.Request{OrderBy: order}, sqlbase.Scope{"tenant": "t"})
			require.Error(t, err)
		})
	}

	require.Empty(t, fake.Calls())
}

func TestMicrosecondFilterAndScope(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"id"}, listing.NewProfile(listing.WithTimestampPrecision(time.Microsecond)))
	require.NoError(t, err)
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, mariadb.ListDialect{}, sqlbase.WithListTimestampPrecision(time.Microsecond), sqlbase.WithListScopeKeys("at"))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "list", listing.Request{Filter: `at == timestamp("2026-01-02T03:04:05.123456Z")`}, sqlbase.Scope{"at": at})
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[0].Query, "DATETIME(6)")
	require.Equal(t, at, fake.Calls()[0].Args[0])
	require.Equal(t, at, fake.Calls()[0].Args[1])
}

func TestQualifiedIndexNameValidation(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "public.items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.NoError(t, err)
	require.Len(t, l.Indexes(), 1)
	require.Contains(t, l.Indexes()[0], `ON "public"."items"`)
	require.NotContains(t, l.Indexes()[0], `""`)

	_, err = sqlbase.NewListEngine[row](db, nil, schema, "public.items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListIndexNamer(func(string, int) string { return `bad"; DROP TABLE items` }))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestRequestFormatterAndOrderPrecedence(t *testing.T) {
	t.Parallel()

	marker := errors.New("formatted")

	var field string

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithErrorFormatter(func(f, reason string, cause error) error { field = f; return errors.Join(marker, cause) })))
	require.NoError(t, err)
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.NoError(t, err)
	_, err = l.List(t.Context(), "list", listing.Request{OrderBy: "id,name", Filter: "invalid (", PageToken: "invalid"}, nil)
	require.ErrorIs(t, err, marker)
	require.Equal(t, listing.FieldOrderBy, field)
	require.Empty(t, fake.Calls())
	_, err = l.List(t.Context(), "list", listing.Request{PageToken: "invalid"}, nil)
	require.ErrorIs(t, err, marker)
	require.Equal(t, pagetoken.TokenField, field)
}

type modifiedOrderParser struct{ key orderby.Key }

func (p modifiedOrderParser) Parse(_ context.Context, text string) (orderby.Spec, error) {
	if text == "modified" {
		return orderby.Spec{Keys: []orderby.Key{p.key}}, nil
	}

	return orderby.Spec{Keys: []orderby.Key{{Name: "name"}}}, nil
}

func TestOrderModifierErrorPrecedence(t *testing.T) {
	t.Parallel()

	for _, key := range []orderby.Key{{Name: "name", Nulls: orderby.NullsFirst}, {Name: "name", Collation: "custom"}} {
		schema := listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, listing.NewProfile(listing.WithUnrestrictedOrder(true), listing.WithOrderParser(modifiedOrderParser{key})))
		db, fake := sqlbasetest.NewFakeDB(t, nil)
		engine, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(v *string) []any { return []any{v} }, postgres.ListDialect{})
		require.NoError(t, err)
		_, err = engine.List(t.Context(), "items", listing.Request{OrderBy: "modified", Filter: "invalid ("}, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "null placement and collation")
		require.NotContains(t, err.Error(), "id may only")
		require.Empty(t, fake.Calls())
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

type domainCodec struct{}

func (domainCodec) ValidateLiteral(v any) error {
	if _, ok := v.(string); !ok {
		return filter.ErrFieldTypeMismatch
	}

	return nil
}

func TestCustomFieldKind(t *testing.T) {
	t.Parallel()

	const domain filter.FieldKind = 100

	schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "domain", Kind: domain}}, []string{"id"}, listing.NewProfile(listing.WithSortFieldValidator(nil), listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{domain: domainCodec{}}))))
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	makeEngine := func(options ...sqlbase.ListEngineOption) error {
		_, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(s *string) []any { return []any{s} }, thirdDialect{}, options...)
		return err
	}
	require.ErrorIs(t, makeEngine(), listing.ErrInvalidSchema)
	require.NoError(t, makeEngine(sqlbase.WithListKindResolver(func(kind filter.FieldKind) sqlbase.Kind {
		if kind == domain {
			return sqlbase.UUID
		}

		return sqlbase.DefaultSQLKind(kind)
	})))
}

func TestCustomKindListAndScope(t *testing.T) {
	t.Parallel()

	const domain filter.FieldKind = 100

	for _, customID := range []bool{false, true} {
		t.Run(fmt.Sprint(customID), func(t *testing.T) {
			t.Parallel()

			idKind := filter.FieldKindString
			if customID {
				idKind = domain
			}

			schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: idKind}, {Name: "domain", Kind: domain}, {Name: "tenant", Kind: domain}}, []string{"domain"}, listing.NewProfile(listing.WithSortFieldValidator(nil), listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{domain: domainCodec{}}))))
			db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				return sqlbasetest.Reply{Columns: []string{"id", "domain", "item"}, Rows: [][]driver.Value{{[]byte("one"), []byte("code"), "one"}}}
			})
			engine, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(s *string) []any { return []any{s} }, postgres.ListDialect{},
				sqlbase.WithListIDKind(idKind), sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListKindResolver(func(kind filter.FieldKind) sqlbase.Kind {
					if kind == domain {
						return sqlbase.Text
					}

					return sqlbase.DefaultSQLKind(kind)
				}))
			require.NoError(t, err)
			page, err := engine.List(t.Context(), "test", listing.Request{}, sqlbase.Scope{"tenant": "code"})
			require.NoError(t, err)
			require.Equal(t, []string{"one"}, page.Items)
		})
	}
}

func TestCustomKindCheckSchema(t *testing.T) {
	t.Parallel()

	const domain filter.FieldKind = 100

	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		t.Run(fmt.Sprintf("%T", dialect), func(t *testing.T) {
			t.Parallel()

			schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: domain}}, []string{"id"}, listing.NewProfile(listing.WithSortFieldValidator(nil),
				listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{domain: domainCodec{}}))))
			db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				if _, ok := dialect.(mariadb.ListDialect); ok {
					return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"},
						Rows: [][]driver.Value{{"id", "varchar", "varchar(64)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil}}}
				}

				return sqlbasetest.Reply{Columns: []string{"name", "type", "udt", "collation", "nullable", "precision"},
					Rows: [][]driver.Value{{"id", "text", "text", "C", "NO", nil}}}
			})
			engine, err := sqlbase.NewListEngine[string](db, nil, schema, "items", []string{"id"}, func(s *string) []any { return []any{s} }, dialect, sqlbase.WithListIDKind(domain), sqlbase.WithListKindResolver(func(kind filter.FieldKind) sqlbase.Kind {
				if kind == domain {
					return sqlbase.Text
				}

				return sqlbase.DefaultSQLKind(kind)
			}))
			require.NoError(t, err)
			require.NoError(t, engine.CheckSchema(t.Context(), db))
		})
	}
}

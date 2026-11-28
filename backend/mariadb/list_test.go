// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func TestMariaDBRejectsUnsupportedIDAndArrays(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		fields  []listing.Field
		option  sqlbase.ListEngineOption
		message string
	}{
		{name: "uuid", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, option: sqlbase.WithListIDType(sqlbase.IDUUID)},
		{name: "array", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}},
		{name: "uuid fields", fields: []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString}},
			option: sqlbase.WithListUUIDFields("owner"), message: "UUID type does not order like the uuid text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, _ := sqlbasetest.NewFakeDB(t, nil)
			schema, err := listing.NewSchema(tc.fields, []string{"id"}, nil)
			require.NoError(t, err)

			var options []sqlbase.ListEngineOption
			if tc.option != nil {
				options = append(options, tc.option)
			}

			_, err = mariadb.NewListEngine[struct{ ID string }](db, nil, schema, "widgets", []string{"id"}, func(r *struct{ ID string }) []any { return []any{&r.ID} }, options...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)

			if tc.message != "" {
				require.ErrorContains(t, err, tc.message)
			}
		})
	}
}

func TestMariaDBFilterScopeAndDecimalCursor(t *testing.T) {
	t.Parallel()

	var seq atomic.Int64

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		if seq.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"id", "price", "id", "price"}, Rows: [][]driver.Value{
				{"z", "10000000000000000.01", "z", "10000000000000000.01"},
				{"a", "10000000000000000.02", "a", "10000000000000000.02"},
			}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "price", "id", "price"}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "price", Kind: filter.FieldKindDecimal}}, []string{"price"}, nil)
	require.NoError(t, err)

	type item struct{ ID, Price string }

	l, err := mariadb.NewListEngine[item](db, pagetokentest.Codec(t), schema, "widgets", []string{"id", "price"}, func(r *item) []any { return []any{&r.ID, &r.Price} }, sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant"))
	require.NoError(t, err)

	request := listing.Request{Parent: "tenant ", PageSize: 1, OrderBy: "price", Filter: `price > 0`}
	first, err := l.List(t.Context(), "maria.List", request, sqlbase.Scope{"tenant": "tenant "})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)
	request.PageToken = first.NextPageToken
	_, err = l.List(t.Context(), "maria.List", request, sqlbase.Scope{"tenant": "tenant "})
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 2)
	require.Contains(t, calls[1].Query, "`_listing`.`price` > CAST(? AS DECIMAL(65,30)) OR (`_listing`.`price` = CAST(? AS DECIMAL(65,30))")
	require.Equal(t, []any{"0", "tenant ", "10000000000000000.01", "10000000000000000.01", "z"}, calls[1].Args)
	require.Contains(t, calls[1].Query, "`tenant` = ?")
	require.Contains(t, calls[1].Query, "ORDER BY `_listing`.`price` ASC, `_listing`.`id` ASC")
}

func TestMariaDBCheckSchemaAndIndexes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, typ, columnType, collation string
		kind                             filter.FieldKind
		array, scope, sort, bad          bool
		precision                        any
	}{
		{name: "timestamp array", typ: "longtext", collation: "utf8mb4_nopad_bin", kind: filter.FieldKindTimestamp, array: true},
		{name: "numeric array", typ: "longtext", collation: "utf8mb4_nopad_bin", kind: filter.FieldKindDecimal, array: true},
		{name: "array pad", typ: "longtext", collation: "utf8mb4_bin", kind: filter.FieldKindInt, array: true, bad: true},
		{name: "scalar pad", typ: "varchar", collation: "utf8mb4_bin", kind: filter.FieldKindString, bad: true},
		{name: "scope longtext ci", typ: "longtext", collation: "utf8mb4_general_ci", scope: true, bad: true},
		{name: "scope tinytext ci", typ: "tinytext", collation: "utf8mb4_general_ci", scope: true, bad: true},
		{name: "scope mediumtext ci", typ: "mediumtext", collation: "utf8mb4_general_ci", scope: true, bad: true},
		{name: "scope text unindexable", typ: "text", collation: "utf8mb4_nopad_bin", scope: true, bad: true},
		{name: "sort text unindexable", typ: "text", collation: "utf8mb4_nopad_bin", kind: filter.FieldKindString, sort: true, bad: true},
		{name: "signed", typ: "bigint", columnType: "bigint(20)", kind: filter.FieldKindInt},
		{name: "unsigned", typ: "bigint", columnType: "bigint(20) unsigned", kind: filter.FieldKindInt, bad: true},
		{name: "varbinary", typ: "varbinary", kind: filter.FieldKindBytes},
		{name: "binary", typ: "binary", kind: filter.FieldKindBytes},
		{name: "tinyblob", typ: "tinyblob", kind: filter.FieldKindBytes},
		{name: "blob", typ: "blob", kind: filter.FieldKindBytes},
		{name: "mediumblob", typ: "mediumblob", kind: filter.FieldKindBytes},
		{name: "longblob", typ: "longblob", kind: filter.FieldKindBytes},
		{name: "datetime", typ: "datetime", kind: filter.FieldKindTimestamp, precision: int64(3)},
		{name: "datetime precision", typ: "datetime", kind: filter.FieldKindTimestamp, precision: int64(6), bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
				if strings.HasPrefix(query, "CREATE INDEX") {
					return sqlbasetest.Reply{}
				}

				return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"}, Rows: [][]driver.Value{
					{"id", "varchar", "varchar(64)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil},
					{"value", tc.typ, tc.columnType, "utf8mb4", tc.collation, "NO", tc.precision},
				}}
			})
			fields := []listing.Field{{Name: "id", Kind: filter.FieldKindString}}

			opts := []sqlbase.ListEngineOption{sqlbase.WithListArrayStorage(sqlbase.ArraysJSON)}
			if tc.scope {
				opts = append(opts, sqlbase.WithListScopeKeys("value"), sqlbase.WithListParentKey("value"))
			} else {
				fields = append(fields, listing.Field{Name: "value", Kind: tc.kind, Array: tc.array})
			}

			orders := []string{"id"}
			if tc.sort {
				orders = []string{"value"}
			}

			schema, err := listing.NewSchema(fields, orders, nil)
			require.NoError(t, err)
			l, err := mariadb.NewListEngine[struct{ ID string }](db, nil, schema, "widgets", []string{"id"}, func(r *struct{ ID string }) []any { return []any{&r.ID} }, opts...)
			require.NoError(t, err)

			err = l.CheckSchema(t.Context(), db)
			if tc.bad {
				require.ErrorIs(t, err, listing.ErrInvalidSchema)
				require.ErrorIs(t, l.EnsureIndexes(t.Context(), db), listing.ErrInvalidSchema)

				return
			}

			require.NoError(t, err)
			require.NoError(t, l.EnsureIndexes(t.Context(), db))
			indexes := l.Indexes()
			require.NotEmpty(t, indexes)

			for _, ddl := range indexes {
				require.Contains(t, ddl, "CREATE INDEX IF NOT EXISTS `")
				require.Contains(t, ddl, "ON `widgets`")
			}

			require.Equal(t, indexes[len(indexes)-1], fake.Calls()[len(fake.Calls())-1].Query)
		})
	}
}

func TestMariaDBSkipChecksForDerivedKey(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"},
			Rows: [][]driver.Value{{"id", "varchar", "varchar(64)", "utf8mb4", "utf8mb4_nopad_bin", "NO", nil}}}
	})
	schema := listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	engine, err := mariadb.NewListEngine(db, nil, schema, "items", []string{"id"}, func(id *string) []any { return []any{id} },
		sqlbase.WithListTableExpression("SELECT id, 'x' AS name FROM items"), sqlbase.WithListDerivedColumns("name"),
		sqlbase.WithListSchemaCheckPolicy(func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
			require.NotEqual(t, "name", r.Column)
			return r.Err
		}))
	require.NoError(t, err)
	require.NoError(t, engine.CheckSchema(t.Context(), db))
}

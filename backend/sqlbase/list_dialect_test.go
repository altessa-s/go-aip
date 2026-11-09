// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// thirdDialect exercises every interface method from outside the package, without
// inheriting either built-in implementation or importing an internal SQL helper.
// This test dialect supports only string identities and the test filter.
type thirdDialect struct{}

var _ sqlbase.ListDialect = thirdDialect{}

func (thirdDialect) ValidIdentifier(s string) bool { return s != "" && !strings.ContainsAny(s, "[]") }

func (thirdDialect) Quote(s string) string            { return "[" + s + "]" }
func (d thirdDialect) Table(s string) (string, error) { return d.Quote(s), nil }
func (thirdDialect) Bind(s string, start int) string {
	for strings.Contains(s, "?") {
		s = strings.Replace(s, "?", fmt.Sprintf("@p%d", start), 1)
		start++
	}

	return s
}

func (thirdDialect) IdentityExpression(column string, _ sqlbase.Kind) string { return column }
func (d thirdDialect) CursorPredicate(keys []sqlbase.ListOrderKey, p pagetoken.Position, alias string, _ sqlbase.IDType, _ sqlbase.DecimalFormat) (string, []any, error) {
	value, ok := p.ID.Value()
	if !ok {
		return "", nil, pagetoken.ErrInvalidToken
	}

	return d.Quote(alias) + "." + d.Quote(keys[0].Column) + " > ? OR " + d.Quote(alias) + "." + d.Quote(keys[0].Column) + " = ?", []any{value, value}, nil
}

func (thirdDialect) Validate(*listing.Schema, sqlbase.ListStorageConfig) error { return nil }
func (d thirdDialect) NewTranslator(*filter.Policy, *sqlbase.Schema, []filter.TranslatorOption, sqlbase.ListTranslatorConfig) (sqlbase.ListFilterTranslator, error) {
	return d, nil
}

func (thirdDialect) Translate(filter.Node) (string, []any, error) {
	return "[id] <> @p1", []any{"excluded"}, nil
}

func (thirdDialect) IndexName(_ string, pattern int) string { return fmt.Sprintf("third_%d", pattern) }
func (thirdDialect) IndexDDL(table, name string, columns []string) string {
	return "CREATE INDEX " + name + " ON " + table + " (" + strings.Join(columns, ", ") + ")"
}

func (thirdDialect) EnsureIndexes(ctx context.Context, db *sql.DB, _ string, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

func (thirdDialect) CheckSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) error {
	rows, err := db.QueryContext(ctx, "SELECT name FROM custom_catalog WHERE table_name = @p1", c.Table)
	if err != nil {
		return err
	}

	return rows.Close()
}

func TestCustomDialect(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64

	db, fake := sqlbasetest.NewFakeDB(t, func(query string, _ []any) sqlbasetest.Reply {
		if strings.HasPrefix(query, "SELECT TOP (2) [id]") && requests.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}, {"b", "b"}}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "id"}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, thirdDialect{}, sqlbase.WithListTableAlias("source"), sqlbase.WithListScopeKeys("tenant"))
	require.NoError(t, err)

	req := listing.Request{PageSize: 1, Filter: `id != "excluded"`}
	first, err := list.List(t.Context(), "custom.List", req, sqlbase.Scope{"tenant": int64(4)})
	require.NoError(t, err)
	require.Equal(t, "a", first.Items[0].ID)
	require.NotEmpty(t, first.NextPageToken)
	req.PageToken = first.NextPageToken
	_, err = list.List(t.Context(), "custom.List", req, sqlbase.Scope{"tenant": int64(4)})
	require.NoError(t, err)
	require.Equal(t, "SELECT TOP (2) [id], [id] FROM [widgets] AS [source] WHERE ([id] <> @p1) AND ([tenant] = @p2) AND ([source].[id] > @p3 OR [source].[id] = @p4) ORDER BY [source].[id] ASC", fake.Calls()[1].Query)
	require.Equal(t, []any{"excluded", int64(4), "a", "a"}, fake.Calls()[1].Args)
	require.Equal(t, []string{"CREATE INDEX [third_0] ON [widgets] ([tenant] ASC, [id] ASC)"}, list.Indexes())
	require.NoError(t, list.EnsureIndexes(t.Context(), db))
	require.Equal(t, "SELECT name FROM custom_catalog WHERE table_name = @p1", fake.Calls()[2].Query)
	require.Equal(t, list.Indexes()[0], fake.Calls()[3].Query)
}

func TestSchemaPolicyAndCollation(t *testing.T) {
	t.Parallel()

	replacementErr := errors.New("replacement rejected column")
	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		for _, tc := range []struct {
			name string
			opts []sqlbase.ListEngineOption
			want error
		}{
			{name: "strict", want: listing.ErrInvalidSchema},
			{name: "custom collation", opts: []sqlbase.ListEngineOption{sqlbase.WithListTextCollation("custom_binary")}},
			{name: "disabled collation", opts: []sqlbase.ListEngineOption{sqlbase.WithListSchemaCheckPolicy(func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
				if r.Check == sqlbase.CheckCollation {
					return nil
				}

				return r.Err
			})}},
			{name: "replacement", opts: []sqlbase.ListEngineOption{sqlbase.WithListTextCollation("custom_binary"), sqlbase.WithListSchemaCheckPolicy(func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
				if r.Check == sqlbase.CheckType {
					return replacementErr
				}

				return r.Err
			})}, want: replacementErr},
		} {
			t.Run(fmt.Sprintf("%T/%s", dialect, tc.name), func(t *testing.T) {
				t.Parallel()
				db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
					if _, ok := dialect.(mariadb.ListDialect); ok {
						return sqlbasetest.Reply{Columns: []string{"name", "type", "column_type", "charset", "collation", "nullable", "precision"}, Rows: [][]driver.Value{{"id", "varchar", "varchar(64)", "utf8mb4", "custom_binary", "NO", nil}}}
					}

					return sqlbasetest.Reply{Columns: []string{"name", "type", "udt", "collation", "nullable", "precision"}, Rows: [][]driver.Value{{"id", "text", "text", "custom_binary", "NO", nil}}}
				})
				schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
				require.NoError(t, err)

				list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, dialect, tc.opts...)
				require.NoError(t, err)

				err = list.CheckSchema(t.Context(), db)
				if tc.want != nil {
					require.ErrorIs(t, err, tc.want)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestDialectAliasAndIndexNaming(t *testing.T) {
	t.Parallel()

	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		t.Run(fmt.Sprintf("%T", dialect), func(t *testing.T) {
			t.Parallel()

			keys := []sqlbase.ListOrderKey{{Column: "age", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindInt), Field: listing.Field{Kind: filter.FieldKindInt}}, {Column: "id", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindString), Field: listing.Field{Kind: filter.FieldKindString}, Desc: true}}
			age, err := pagetoken.NewSortValue(int64(2))
			require.NoError(t, err)
			id, err := pagetoken.NewSortValue("a")
			require.NoError(t, err)
			pred, _, err := dialect.CursorPredicate(keys, pagetoken.Position{Keys: []pagetoken.SortValue{age}, ID: id}, "source", sqlbase.IDNative, sqlbase.DefaultDecimalFormat())
			require.NoError(t, err)
			require.NotContains(t, pred, "_listing")
			require.Contains(t, pred, dialect.Quote("source")+"."+dialect.Quote("age"))
			require.Contains(t, pred, dialect.Quote("source")+"."+dialect.Quote("id"))
			db, fake := sqlbasetest.NewFakeDB(t, nil)
			schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
			require.NoError(t, err)
			list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, dialect, sqlbase.WithListTableAlias("source"), sqlbase.WithListIndexNamer(func(string, int) string { return "custom_index" }))
			require.NoError(t, err)
			require.Contains(t, list.Indexes()[0], dialect.Quote("custom_index"))
			_, err = list.List(t.Context(), "alias.List", listing.Request{}, nil)
			require.NoError(t, err)
			require.Contains(t, fake.Calls()[0].Query, " AS "+dialect.Quote("source"))
		})
	}
}

func TestSchemaPolicyDoesNotSkipLaterChecks(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"name", "type", "udt", "collation", "nullable", "precision"}, Rows: [][]driver.Value{
			{"id", "text", "text", "wrong", "NO", nil}, {"at", "timestamp with time zone", "timestamptz", nil, "NO", int64(6)},
		}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"id"}, nil)
	require.NoError(t, err)

	policy := sqlbase.WithListSchemaCheckPolicy(func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
		if r.Check == sqlbase.CheckCollation {
			return nil
		}

		return r.Err
	})
	newList := func(opts ...sqlbase.ListEngineOption) *sqlbase.ListEngine[row] {
		t.Helper()

		list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, opts...)
		require.NoError(t, err)

		return list
	}
	require.ErrorIs(t, newList(policy).CheckSchema(t.Context(), db), listing.ErrInvalidSchema)
	require.NoError(t, newList(policy, sqlbase.WithListTimestampPrecision(time.Microsecond)).CheckSchema(t.Context(), db))
}

// options returns option as a list, empty for a nil option.
func options(option sqlbase.ListEngineOption) []sqlbase.ListEngineOption {
	if option == nil {
		return nil
	}

	return []sqlbase.ListEngineOption{option}
}

func TestCustomizationValidation(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"id", "name"}, nil)
	require.NoError(t, err)

	var nilDialect *thirdDialect
	for _, tc := range []struct {
		name    string
		dialect sqlbase.ListDialect
		option  sqlbase.ListEngineOption
	}{
		{"nil dialect", nil, nil},
		{"typed nil dialect", nilDialect, nil},
		{"invalid alias", postgres.ListDialect{}, sqlbase.WithListTableAlias("a.b")},
		{"invalid index", postgres.ListDialect{}, sqlbase.WithListIndexNamer(func(string, int) string { return "bad;name" })},
		{"duplicate index", postgres.ListDialect{}, sqlbase.WithListIndexNamer(func(string, int) string { return "same" })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, tc.dialect, options(tc.option)...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}
}

func (thirdDialect) LimitSelect(query string, limit int) string {
	return strings.Replace(query, "SELECT ", fmt.Sprintf("SELECT TOP (%d) ", limit), 1)
}

func TestDialectOwnsIdentifierGrammar(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)

	name := strings.Repeat("x", 64)
	_, err = sqlbase.NewListEngine[row](db, nil, schema, "items", []string{name}, func(r *row) []any { return []any{&r.ID} }, thirdDialect{}, sqlbase.WithListIDColumn(name))
	require.NoError(t, err)
}

func TestCustomDialectColumnGrammar(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{} })
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	_, err = sqlbase.NewListEngine[string](db, nil, schema, "records", []string{"record-id"}, func(r *string) []any { return []any{r} }, thirdDialect{}, sqlbase.WithListColumns(map[string]string{"id": "record-id"}), sqlbase.WithListIDColumn("record-id"))
	require.NoError(t, err)
}

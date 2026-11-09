// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

type failingQuerier struct{ err error }

func (q failingQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, q.err
}

type serverCancelQuerier struct{}

func (serverCancelQuerier) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	<-ctx.Done()
	return nil, errors.New("server canceled statement")
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "database timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

type row struct {
	ID, Name, Owner string
	Age             int64
}

type cursorRow struct {
	ID    string
	Value any
}

func TestListBuildsPostgresQueryAndScope(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "name", "owner"}}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString},
	}, []string{"name"}, nil)
	require.NoError(t, err)
	list, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id", "name", "owner"}, func(r *row) []any { return []any{&r.ID, &r.Name, &r.Owner} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("owner"), sqlbase.WithListParentKey("owner"))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "widgets.List", listing.Request{PageSize: 5, Parent: "tenant-a", OrderBy: "name", Filter: `name.startsWith("a")`}, sqlbase.Scope{"owner": "tenant-a"})
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	call := calls[0]
	require.Contains(t, call.Query, `"name"`)
	require.Contains(t, call.Query, `"owner" = $2`)
	require.Contains(t, call.Query, `ORDER BY "_listing"."name" ASC, "_listing"."id" ASC`)
	require.Equal(t, []any{"a", "tenant-a"}, call.Args)
}

func TestNewRejectsInvalidDestinations(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(*row) []any { return nil }, postgres.ListDialect{})
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(*row) []any { return []any{nil} }, postgres.ListDialect{})
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestNewRejectsInvalidProjectionAndNullableScopeKeys(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	for _, tc := range []struct {
		name    string
		fields  []listing.Field
		columns []string
		options []sqlbase.ListEngineOption
	}{
		{"invalid projection", []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id; DROP TABLE widgets"}, nil},
		{"empty projection", []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, nil, nil},
		{"array scope", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tags"), sqlbase.WithListParentKey("tags")}},
		{"bytes scope", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "blob", Kind: filter.FieldKindBytes}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("blob"), sqlbase.WithListParentKey("blob")}},
		{"presence scope", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tenant", Kind: filter.FieldKindString, Presence: true}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema, err := listing.NewSchema(tc.fields, []string{"id"}, nil)
			require.NoError(t, err)
			_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", tc.columns, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, tc.options...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}
}

func TestNewRejectsUnsafeSortPatterns(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	for _, tc := range []struct {
		name     string
		fields   []listing.Field
		patterns []string
		options  []sqlbase.ListEngineOption
	}{
		{"id in middle", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"id, name"}, nil},
		{"duplicate mapped column", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "title", Kind: filter.FieldKindString}}, []string{"name, title"}, []sqlbase.ListEngineOption{sqlbase.WithListColumns(map[string]string{"name": "value", "title": "value"})}},
		{"scope column", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tenant", Kind: filter.FieldKindString}}, []string{"tenant"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant")}},
		{"duplicate scope columns", []listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("org", "tenant"), sqlbase.WithListColumns(map[string]string{"org": "tenant_id", "tenant": "tenant_id"})}},
		{"scope collides with presence column", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "note", Kind: filter.FieldKindString, Presence: true}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant"), sqlbase.WithListColumns(map[string]string{"note": "tenant_id", "tenant": "tenant_id"})}},
		{"scope collides with array column", []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}, []string{"id"}, []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListParentKey("tenant"), sqlbase.WithListColumns(map[string]string{"tags": "tenant_id", "tenant": "tenant_id"})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema, err := listing.NewSchema(tc.fields, tc.patterns, nil)
			require.NoError(t, err)
			_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, tc.options...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}
}

func TestCheckSchemaUsesTimestampArrayElementPrecision(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"column_name", "data_type", "udt_name", "collation_name", "is_nullable", "datetime_precision"}, Rows: [][]driver.Value{
			{"id", "text", "text", "C", "NO", nil}, {"events", "ARRAY", "_timestamptz", "", "NO", int64(3)},
		}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "events", Kind: filter.FieldKindTimestamp, Array: true}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.NoError(t, err)
	require.NoError(t, l.CheckSchema(t.Context(), db))
}

func TestWithIDColumnMapsFilterSortAndCursor(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		switch calls.Add(1) {
		case 1:
			return sqlbasetest.Reply{Columns: []string{"widget_id", "widget_id", "name"}, Rows: [][]driver.Value{{"w1", "w1", "alpha"}}}
		case 2:
			return sqlbasetest.Reply{Columns: []string{"widget_id", "name", "widget_id", "name"}, Rows: [][]driver.Value{{"w1", "alpha", "w1", "alpha"}, {"w2", "beta", "w2", "beta"}}}
		default:
			return sqlbasetest.Reply{Columns: []string{"widget_id", "name", "widget_id", "name"}}
		}
	})
	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString},
	}, []string{"name", "id"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "widgets", []string{"widget_id", "name"},
		func(r *row) []any { return []any{&r.ID, &r.Name} }, postgres.ListDialect{}, sqlbase.WithListIDColumn("widget_id"), sqlbase.WithListColumns(map[string]string{"id": "widget_id"}))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "id", Filter: `id == "w1"`}, nil)
	require.NoError(t, err)

	query := fake.Calls()[0].Query
	require.Contains(t, query, `"widget_id" = ($1)::text`)
	require.Contains(t, query, `ORDER BY "_listing"."widget_id" ASC`)
	require.NotContains(t, query, `"id" =`)

	first, err := l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", PageToken: first.NextPageToken}, nil)
	require.NoError(t, err)

	query = fake.Calls()[2].Query
	require.Contains(t, query, `ORDER BY "_listing"."name" ASC, "_listing"."widget_id" ASC`)
	require.Equal(t, []any{"alpha", "w1"}, fake.Calls()[2].Args)
}

func TestUUIDIDTypeFollowsMappedIDColumn(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"widget_id"}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "widgetId", Kind: filter.FieldKindString}}, []string{"widgetId"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"widget_id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{},
		sqlbase.WithListIDColumn("widget_id"), sqlbase.WithListColumns(map[string]string{"widgetId": "widget_id"}), sqlbase.WithListIDType(sqlbase.IDUUID))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, Filter: `widgetId == "00000001-0000-0000-0000-000000000000"`}, nil)
	require.NoError(t, err)
	require.Contains(t, fake.Calls()[0].Query, `"widget_id" = ($1)::uuid`)
}

func TestNewRejectsInvalidIDColumnField(t *testing.T) {
	t.Parallel()

	db, _ := sqlbasetest.NewFakeDB(t, nil)
	for _, field := range []listing.Field{
		{Name: "widgetId", Kind: filter.FieldKindInt},
		{Name: "widgetId", Kind: filter.FieldKindString, Optional: true},
		{Name: "widgetId", Kind: filter.FieldKindString, Array: true},
		{Name: "widgetId", Kind: filter.FieldKindString, Presence: true},
	} {
		t.Run(field.Kind.String()+field.Name, func(t *testing.T) {
			t.Parallel()

			field.Name = "widgetId"
			schema, err := listing.NewSchema([]listing.Field{field, {Name: "rank", Kind: filter.FieldKindString}}, []string{"rank"}, nil)
			require.NoError(t, err)
			_, err = sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"widget_id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{},
				sqlbase.WithListIDColumn("widget_id"), sqlbase.WithListColumns(map[string]string{"widgetId": "widget_id"}))
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}
}

func TestCheckSchemaMapsDeclaredIDToConfiguredColumn(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"column_name", "data_type", "udt_name", "collation_name", "is_nullable", "datetime_precision"},
			Rows: [][]driver.Value{{"widget_id", "text", "text", "C", "NO", nil}, {"name", "text", "text", "C", "NO", nil}}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"widget_id", "name"}, func(r *row) []any { return []any{&r.ID, &r.Name} }, postgres.ListDialect{}, sqlbase.WithListIDColumn("widget_id"), sqlbase.WithListColumns(map[string]string{"id": "widget_id"}))
	require.NoError(t, err)
	require.NoError(t, l.CheckSchema(t.Context(), db))
}

func TestCheckSchemaRejectsMissingColumn(t *testing.T) {
	t.Parallel()
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"column_name", "data_type", "udt_name", "collation_name", "is_nullable", "datetime_precision"}, Rows: [][]driver.Value{{"id", "text", "text", "C", "NO", nil}}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.NoError(t, err)
	require.ErrorIs(t, l.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema)
}

func TestCheckSchemaValidatesProjectionAndScopeColumns(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)

	rows := func(nullable string) sqlbasetest.Reply {
		return sqlbasetest.Reply{
			Columns: []string{"column_name", "data_type", "udt_name", "collation_name", "is_nullable", "datetime_precision"},
			Rows:    [][]driver.Value{{"id", "text", "text", "C", "NO", nil}, {"tenant_id", "text", "text", "C", nullable, nil}},
		}
	}
	for _, tc := range []struct {
		name       string
		reply      sqlbasetest.Reply
		projection string
		scope      bool
	}{
		{"nullable scope", rows("YES"), "id", true},
		{"missing projection", sqlbasetest.Reply{Columns: rows("NO").Columns, Rows: [][]driver.Value{{"id", "text", "text", "C", "NO", nil}}}, "missing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return tc.reply })

			opts := []sqlbase.ListEngineOption{}
			if tc.scope {
				opts = append(opts, sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"))
			}

			l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{tc.projection}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, opts...)
			require.NoError(t, err)
			require.ErrorIs(t, l.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema)
		})
	}
}

func TestCheckSchemaValidatesTextScopeCollation(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{
			Columns: []string{"column_name", "data_type", "udt_name", "collation_name", "is_nullable", "datetime_precision"},
			Rows: [][]driver.Value{
				{"id", "text", "text", "C", "NO", nil},
				{"tenant_id", "text", "text", "C.UTF-8", "NO", nil},
			},
		}
	})
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"))
	require.NoError(t, err)
	require.ErrorIs(t, l.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema)
}

func TestQueryTimeoutClassification(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	newList := func(err error) *sqlbase.ListEngine[row] {
		l, newErr := sqlbase.NewListEngine[row](failingQuerier{err: err}, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
		require.NoError(t, newErr)

		return l
	}

	t.Run("filtered deadline", func(t *testing.T) {
		t.Parallel()
		_, err := newList(context.DeadlineExceeded).List(t.Context(), "widgets.List", listing.Request{Filter: `id == "x"`}, nil)
		fieldErr, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok)
		require.Equal(t, listing.FieldFilter, fieldErr.Field)
		require.ErrorIs(t, err, sqlbase.ErrTimeout)
	})
	t.Run("filtered network timeout", func(t *testing.T) {
		t.Parallel()
		_, err := newList(timeoutError{}).List(t.Context(), "widgets.List", listing.Request{Filter: `id == "x"`}, nil)
		fieldErr, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok)
		require.Equal(t, listing.FieldFilter, fieldErr.Field)
		require.ErrorIs(t, err, sqlbase.ErrTimeout)
	})
	t.Run("unfiltered deadline", func(t *testing.T) {
		t.Parallel()
		_, err := newList(context.DeadlineExceeded).List(t.Context(), "widgets.List", listing.Request{}, nil)
		require.ErrorIs(t, err, coreerrs.ErrUnavailable)
		require.ErrorIs(t, err, sqlbase.ErrTimeout)
	})
	t.Run("caller cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := newList(context.DeadlineExceeded).List(ctx, "widgets.List", listing.Request{Filter: `id == "x"`}, nil)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, sqlbase.ErrTimeout)
	})
	// A driver error reporting the timeout stays hidden: a FieldError or sentinel it joins selects no status of its own.
	t.Run("driver error hidden", func(t *testing.T) {
		t.Parallel()

		hidden := coreerrs.NewFieldError("storedField", "secret")
		driverErr := errors.Join(context.DeadlineExceeded, hidden)

		for _, filtered := range []bool{false, true} {
			req := listing.Request{}
			if filtered {
				req.Filter = `id == "x"`
			}

			_, err := newList(driverErr).List(t.Context(), "widgets.List", req, nil)
			require.ErrorIs(t, err, sqlbase.ErrTimeout)
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			require.NotErrorIs(t, err, hidden)
			require.NotContains(t, err.Error(), "secret")
			require.ErrorIs(t, apierror.HiddenCause(err), hidden)

			fieldErr, ok := errors.AsType[*coreerrs.FieldError](err)
			require.Equal(t, filtered, ok)

			if filtered {
				require.Equal(t, listing.FieldFilter, fieldErr.Field)
			}
		}
	})
}

func TestQueryServerCancelAfterMaxTimeMapsToTimeout(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](serverCancelQuerier{}, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListMaxTime(time.Millisecond))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{Filter: `id == "x"`}, nil)
	fieldErr, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, listing.FieldFilter, fieldErr.Field)
	require.ErrorIs(t, err, sqlbase.ErrTimeout)
}

func TestScopeMustPinDeclaredKeys(t *testing.T) {
	t.Parallel()
	db, fake := sqlbasetest.NewFakeDB(t, nil)
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "owner", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("owner"), sqlbase.WithListParentKey("owner"))
	require.NoError(t, err)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{Parent: "tenant"}, nil)
	require.ErrorIs(t, err, sqlbase.ErrScope)
	require.Empty(t, fake.Calls())
}

func TestCursorMixedDirectionSQL(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		if calls.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"id", "name", "age", "id", "name", "age"}, Rows: [][]driver.Value{{"a", "alpha", int64(7), "a", "alpha", int64(7)}, {"b", "alpha", int64(5), "b", "alpha", int64(5)}}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "name", "age", "id", "name", "age"}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}, {Name: "age", Kind: filter.FieldKindInt}}, []string{"name, age desc"}, nil)
	require.NoError(t, err)
	list, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "widgets", []string{"id", "name", "age"}, func(r *row) []any { return []any{&r.ID, &r.Name, &r.Age} }, postgres.ListDialect{})
	require.NoError(t, err)
	first, err := list.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name, age desc"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)
	_, err = list.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name, age desc", PageToken: first.NextPageToken}, nil)
	require.NoError(t, err)

	query := fake.Calls()[1].Query
	require.Contains(t, query, `"_listing"."name" > $1 OR ("_listing"."name" = $2`)
	require.Contains(t, query, `"_listing"."age" < $3 OR ("_listing"."age" = $4`)
	require.Contains(t, query, `"_listing"."id" < $5`)
	require.Equal(t, []any{"alpha", "alpha", int64(7), int64(7), "a"}, fake.Calls()[1].Args)
}

func TestCursorBindsEverySortType(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, 4, 3, 2, 1, 0, 123000000, time.FixedZone("east", 5*60*60))
	for _, tc := range []struct {
		name        string
		kind        filter.FieldKind
		value, want any
	}{
		{"string", filter.FieldKindString, "alpha", "alpha"},
		{"integer", filter.FieldKindInt, int64(17), int64(17)},
		{"float", filter.FieldKindFloat, float64(2.5), float64(2.5)},
		{"bool", filter.FieldKindBool, true, true},
		{"timestamp", filter.FieldKindTimestamp, instant, instant.UTC().Truncate(listing.TimestampPrecision)},
		{"decimal", filter.FieldKindDecimal, "1.25", "1.25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				if calls.Add(1) == 1 {
					return sqlbasetest.Reply{Columns: []string{"id", "value", "id", "value"}, Rows: [][]driver.Value{{"a", tc.value, "a", tc.value}, {"b", tc.value, "b", tc.value}}}
				}

				return sqlbasetest.Reply{Columns: []string{"id", "value", "id", "value"}}
			})
			schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "value", Kind: tc.kind}}, []string{"value"}, nil)
			require.NoError(t, err)
			l, err := sqlbase.NewListEngine[cursorRow](db, pagetokentest.Codec(t), schema, "typed_values", []string{"id", "value"}, func(r *cursorRow) []any { return []any{&r.ID, &r.Value} }, postgres.ListDialect{})
			require.NoError(t, err)
			first, err := l.List(t.Context(), "typed.List", listing.Request{PageSize: 1, OrderBy: "value"}, nil)
			require.NoError(t, err)
			require.NotEmpty(t, first.NextPageToken)
			_, err = l.List(t.Context(), "typed.List", listing.Request{PageSize: 1, OrderBy: "value", PageToken: first.NextPageToken}, nil)
			require.NoError(t, err)

			call := fake.Calls()[1]
			require.Contains(t, call.Query, `("_listing"."value", "_listing"."id") > ($1, $2)`)
			require.Equal(t, []any{tc.want, "a"}, call.Args)
		})
	}
}

func TestProjectionRepeatsIDAndSortKey(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		if calls.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"id", "name", "id", "name"}, Rows: [][]driver.Value{{"a", "alpha", "a", "alpha"}, {"b", "bravo", "b", "bravo"}}}
		}

		return sqlbasetest.Reply{Columns: []string{"id", "name", "id", "name"}}
	})
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, nil)
	require.NoError(t, err)
	l, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "widgets", []string{"id", "name"}, func(r *row) []any { return []any{&r.ID, &r.Name} }, postgres.ListDialect{})
	require.NoError(t, err)
	first, err := l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)
	_, err = l.List(t.Context(), "widgets.List", listing.Request{PageSize: 1, OrderBy: "name", PageToken: first.NextPageToken}, nil)
	require.NoError(t, err)

	callsMade := fake.Calls()
	require.Contains(t, callsMade[0].Query, `ORDER BY "_listing"."name" ASC, "_listing"."id" ASC`)
	require.Contains(t, callsMade[1].Query, `("_listing"."name", "_listing"."id") > ($1, $2)`)
}

func TestIndependentScopeAndErrorMapper(t *testing.T) {
	t.Parallel()

	cause := errors.New("query failed")
	mapped := errors.New("application mapping")
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)
	list, err := sqlbase.NewListEngine[row](failingQuerier{cause}, nil, schema, "widgets", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, sqlbase.WithListScopeKeys("tenant"), sqlbase.WithListMaxTime(0), sqlbase.WithListErrorMapper(func(ctx context.Context, err error, filtered bool) error {
		require.ErrorIs(t, err, cause)
		return errors.Join(mapped, err)
	}))
	require.NoError(t, err)
	_, err = list.List(t.Context(), "widgets", listing.Request{}, sqlbase.Scope{"tenant": int64(42)})
	require.ErrorIs(t, err, mapped)
	require.ErrorIs(t, err, cause)
}

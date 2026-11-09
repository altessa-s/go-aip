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
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func sortValue(t *testing.T, v any) pagetoken.SortValue {
	t.Helper()

	sv, err := pagetoken.NewSortValue(v)
	require.NoError(t, err)

	return sv
}

func TestDecodeCursor(t *testing.T) {
	t.Parallel()

	keys := []sqlbase.ListOrderKey{{Column: "price", StorageKind: sqlbase.Numeric}, {Column: "id", StorageKind: sqlbase.Integer}}
	pos := pagetoken.Position{Keys: []pagetoken.SortValue{sortValue(t, decimal.RequireFromString("1.5"))}, ID: sortValue(t, int64(7))}
	errTooWide := errors.New("too wide")

	for _, tc := range []struct {
		name  string
		keys  []sqlbase.ListOrderKey
		pos   pagetoken.Position
		check sqlbase.CursorValueCheck
		want  []any
		err   error
	}{
		{name: "values", keys: keys, pos: pos, want: []any{decimal.RequireFromString("1.5"), int64(7)}},
		{name: "no keys", pos: pos, err: pagetoken.ErrInvalidToken},
		{name: "missing sort value", keys: keys, pos: pagetoken.Position{ID: pos.ID}, err: pagetoken.ErrInvalidToken},
		{name: "kind mismatch", keys: keys, pos: pagetoken.Position{Keys: []pagetoken.SortValue{sortValue(t, "x")}, ID: pos.ID},
			err: pagetoken.ErrInvalidToken},
		{name: "check rejects", keys: keys, pos: pos, check: func(k sqlbase.ListOrderKey, _ any) error {
			if k.StorageKind == sqlbase.Numeric {
				return errTooWide
			}

			return nil
		}, err: errTooWide},
		{name: "check rejects identity", keys: keys, pos: pos, check: func(k sqlbase.ListOrderKey, _ any) error {
			if k.Column == "id" {
				return errTooWide
			}

			return nil
		}, err: errTooWide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.DecodeCursor(tc.keys, tc.pos, tc.check)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, err, pagetoken.ErrInvalidToken)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestExpandKeysetPredicate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		desc  []bool
		query string
		args  []any
	}{
		{name: "empty"},
		{name: "one key", desc: []bool{true}, query: "(c0 < m0)", args: []any{0}},
		{name: "two keys", desc: []bool{false, true}, query: "((c0 > m0 OR (c0 = m0 AND c1 < m1)))", args: []any{0, 0, 1}},
		{name: "three keys", desc: []bool{false, false, true},
			query: "((c0 > m0 OR (c0 = m0 AND (c1 > m1 OR (c1 = m1 AND c2 < m2)))))", args: []any{0, 0, 1, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keys := make([]sqlbase.ListOrderKey, len(tc.desc))
			cols := make([]string, len(tc.desc))
			marks := make([]string, len(tc.desc))
			vals := make([]any, len(tc.desc))

			for i, desc := range tc.desc {
				keys[i] = sqlbase.ListOrderKey{Column: fmt.Sprint(i), Desc: desc}
				cols[i], marks[i], vals[i] = fmt.Sprintf("c%d", i), fmt.Sprintf("m%d", i), i
			}

			query, args := sqlbase.ExpandKeysetPredicate(keys, cols, marks, vals)
			require.Equal(t, tc.query, query)
			require.Equal(t, tc.args, args)
		})
	}
}

func TestListSchemaConfigHelpers(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "rank", Kind: filter.FieldKindInt},
	}, []string{"name", "rank desc"}, nil)
	c := sqlbase.ListSchemaConfig{
		Schema: schema, Columns: map[string]string{"name": "display_name"}, IDColumn: "id", IDKind: filter.FieldKindString,
		Storage: sqlbase.ListStorageConfig{DecimalFormat: sqlbase.DecimalFormat{Precision: 10, Scale: 2}}, TimestampPrecision: time.Millisecond,
	}

	require.Equal(t, "display_name", c.Column("name"))
	require.Equal(t, "rank", c.Column("rank"))
	require.Equal(t, sqlbase.Integer, c.Kind(filter.FieldKindInt))
	require.Equal(t, sqlbase.Text, sqlbase.ListSchemaConfig{KindResolver: func(filter.FieldKind) sqlbase.Kind { return sqlbase.Text }}.Kind(filter.FieldKindInt))

	require.True(t, c.AlignsTimestamps(3))
	require.True(t, c.AlignsTimestamps(0))
	require.False(t, c.AlignsTimestamps(6), "a microsecond column holds timestamps between milliseconds")

	require.True(t, c.FitsDecimal(10, 2))
	require.True(t, c.FitsDecimal(8, 1))
	require.False(t, c.FitsDecimal(12, 2), "more integer digits")
	require.False(t, c.FitsDecimal(10, 3), "more fractional digits")

	keys := c.OrderKeys(schema.Orders()[0])
	require.Equal(t, []string{"display_name", "id"}, []string{keys[0].Column, keys[1].Column})
	require.Equal(t, sqlbase.Text, keys[0].StorageKind)
	require.Equal(t, listing.Field{Name: "name", Kind: filter.FieldKindString}, keys[0].Field)

	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return sqlbasetest.Reply{} })
	require.NoError(t, c.Check(t.Context(), db, sqlbase.CheckType, "x", false, "unused"))

	err := c.Check(t.Context(), db, sqlbase.CheckType, "x", true, "column %q is wrong", "x")
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.ErrorContains(t, err, `column "x" is wrong`)

	var seen []sqlbase.ListSchemaCheckResult

	c.CheckPolicy = func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
		seen = append(seen, r)
		return nil
	}
	require.NoError(t, c.Check(t.Context(), db, sqlbase.CheckCollation, "x", true, "replaced"))
	require.Len(t, seen, 1)
	require.Equal(t, sqlbase.CheckCollation, seen[0].Check)
	require.ErrorIs(t, seen[0].Err, listing.ErrInvalidSchema)
}

// recordingChecker records the dialect checks CheckListColumns delegates.
type recordingChecker struct {
	calls *[]string
	err   error
}

func (r recordingChecker) CheckScopeColumn(_ context.Context, _ *sql.DB, column string, meta string) error {
	*r.calls = append(*r.calls, "scope "+column+" "+meta)
	return r.err
}

func (r recordingChecker) CheckColumn(_ context.Context, _ *sql.DB, column string, f listing.Field, meta string, scoped bool) error {
	*r.calls = append(*r.calls, fmt.Sprintf("column %s %s %v %v", column, meta, f.Kind, scoped))
	return r.err
}

func TestCheckListColumns(t *testing.T) {
	t.Parallel()

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "tenant", Kind: filter.FieldKindString},
		{Name: "rank", Kind: filter.FieldKindInt},
	}, []string{"rank"}, nil)
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"x"}, Rows: [][]driver.Value{}}
	})
	base := sqlbase.ListSchemaConfig{
		Schema: schema, Columns: map[string]string{"tenant": "tenant_id"}, Projection: []string{"payload"},
		ScopeKeys: []string{"tenant"}, IDColumn: "id", IDKind: filter.FieldKindString,
	}
	all := map[string]string{"payload": "p", "tenant_id": "t", "rank": "r", "id": "i"}

	t.Run("all present", func(t *testing.T) {
		t.Parallel()

		var calls []string

		scope, err := sqlbase.CheckListColumns(t.Context(), db, base, all, recordingChecker{calls: &calls})
		require.NoError(t, err)
		require.Equal(t, map[string]bool{"tenant_id": true}, scope)
		require.Equal(t, []string{
			"scope tenant_id t",
			"column id i string false",
			"column rank r int false",
			"column tenant_id t string true",
		}, calls)
	})

	t.Run("missing columns", func(t *testing.T) {
		t.Parallel()

		for _, missing := range []string{"payload", "tenant_id", "rank", "id"} {
			got := map[string]string{}

			for k, v := range all {
				if k != missing {
					got[k] = v
				}
			}

			var calls []string

			_, err := sqlbase.CheckListColumns(t.Context(), db, base, got, recordingChecker{calls: &calls})
			require.ErrorIs(t, err, listing.ErrInvalidSchema, missing)
			require.ErrorContains(t, err, fmt.Sprintf("%q", missing))
		}
	})

	t.Run("policy keeps checking", func(t *testing.T) {
		t.Parallel()

		var results []string

		c := base
		c.CheckPolicy = func(_ context.Context, _ *sql.DB, r sqlbase.ListSchemaCheckResult) error {
			results = append(results, fmt.Sprintf("%s %s %v", r.Check, r.Column, r.Err != nil))
			return nil
		}

		var calls []string

		scope, err := sqlbase.CheckListColumns(t.Context(), db, c, map[string]string{"rank": "r"}, recordingChecker{calls: &calls})
		require.NoError(t, err)
		require.Empty(t, scope)
		require.Equal(t, []string{"column rank r int false"}, calls)
		require.Equal(t, []string{
			"column payload true", "column tenant_id true", "column id true", "column rank false", "column tenant_id true",
		}, results)
	})

	t.Run("checker error", func(t *testing.T) {
		t.Parallel()

		errDialect := errors.New("dialect check")

		var calls []string

		_, err := sqlbase.CheckListColumns(t.Context(), db, base, all, recordingChecker{calls: &calls, err: errDialect})
		require.ErrorIs(t, err, errDialect)
		require.Equal(t, []string{"scope tenant_id t"}, calls)

		c := base
		c.ScopeKeys = nil
		calls = nil
		_, err = sqlbase.CheckListColumns(t.Context(), db, c, all, recordingChecker{calls: &calls, err: errDialect})
		require.ErrorIs(t, err, errDialect)
		require.Equal(t, []string{"column id i string false"}, calls)
	})
}

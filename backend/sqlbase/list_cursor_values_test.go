// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
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

type domainID string

func (d domainID) Value() (driver.Value, error) { return string(d), nil }
func TestOpaqueCursorSQLValue(t *testing.T) {
	t.Parallel()

	for _, d := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		_, args, err := d.CursorPredicate([]sqlbase.ListOrderKey{{Column: "id", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindString), Field: listing.Field{Kind: filter.FieldKindString}}}, pagetoken.Position{ID: pagetoken.OpaqueSortValue(domainID("abc"))}, "t", sqlbase.IDNative, sqlbase.DefaultDecimalFormat())
		require.NoError(t, err)
		require.Equal(t, []any{"abc"}, args)
	}
}

func TestMariaDBCursorDecimalDomain(t *testing.T) {
	t.Parallel()

	bad, err := pagetoken.NewSortValue(decimal.NewFromInt(100))
	require.NoError(t, err)
	id, err := pagetoken.NewSortValue("id")
	require.NoError(t, err)

	for _, identity := range []bool{false, true} {
		ks := []sqlbase.ListOrderKey{{Column: "v", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindDecimal), Field: listing.Field{Kind: filter.FieldKindDecimal}}, {Column: "id", StorageKind: sqlbase.DefaultSQLKind(filter.FieldKindString), Field: listing.Field{Kind: filter.FieldKindString}}}
		pos := pagetoken.Position{Keys: []pagetoken.SortValue{bad}, ID: id}

		if identity {
			ks = ks[:1]
			pos = pagetoken.Position{ID: bad}
		}

		_, _, err := (mariadb.ListDialect{}).CursorPredicate(ks, pos, "t", sqlbase.IDNative, sqlbase.DecimalFormat{Precision: 2, Scale: 0})
		require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
		require.NotEmpty(t, pagetoken.Classify(err).String())
	}
}

type domainPositionCodec struct{ pagetoken.ScalarPositionCodec }

func (domainPositionCodec) Name() string { return "domain-id-v1" }
func (domainPositionCodec) Normalize(v any, _ time.Duration) (pagetoken.SortValue, error) {
	s, ok := v.(string)
	if !ok {
		return pagetoken.SortValue{}, pagetoken.ErrUnsortableValue
	}

	return pagetoken.OpaqueSortValue(domainID(s)), nil
}

func (domainPositionCodec) Compare(a, b pagetoken.SortValue) int {
	av, _ := a.Value()
	bv, _ := b.Value()

	return strings.Compare(string(av.(domainID)), string(bv.(domainID)))
}

func (domainPositionCodec) Marshal(v any) ([]byte, error) {
	id, _ := v.(pagetoken.Position).ID.Value()
	return json.Marshal(id)
}

func (domainPositionCodec) Unmarshal(data []byte, v any) error {
	var id domainID
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}

	*v.(*pagetoken.Position) = pagetoken.Position{ID: pagetoken.OpaqueSortValue(id)}

	return nil
}

func TestOpaqueCodecNextPage(t *testing.T) {
	t.Parallel()

	for _, dialect := range []sqlbase.ListDialect{postgres.ListDialect{}, mariadb.ListDialect{}} {
		db, fake := sqlbasetest.NewFakeDB(t, func(_ string, args []any) sqlbasetest.Reply {
			rows := [][]driver.Value{{"a", "a"}, {"b", "b"}}
			if len(args) > 0 {
				rows = rows[1:]
			}

			return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: rows}
		})
		schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
		require.NoError(t, err)
		l, err := sqlbase.NewListEngine[row](db, pagetokentest.Codec(t), schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, dialect, sqlbase.WithListPositionCodec(domainPositionCodec{}))
		require.NoError(t, err)
		first, err := l.List(t.Context(), "list", listing.Request{PageSize: 1}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, first.NextPageToken)
		next, err := l.List(t.Context(), "list", listing.Request{PageSize: 1, PageToken: first.NextPageToken}, nil)
		require.NoError(t, err)
		require.Len(t, next.Items, 1)
		require.Equal(t, "b", next.Items[0].ID)
		require.Equal(t, []any{"a"}, fake.Calls()[1].Args)
	}
}

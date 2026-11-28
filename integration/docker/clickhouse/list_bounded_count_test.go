// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

// TestBoundedCount counts exactly up to the limit and, without a planner estimate, reports the counted rows as a lower
// bound above it; ClickHouse cannot bound a probe, so a probe time is refused before any statement runs.
func TestBoundedCount(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	items := listingtest.MakeWidgets(6)
	storeWidgets(t, db, "widgets", items)
	engine, err := clickhouse.NewListEngine(db, nil, listingtest.NewSchema(t), "widgets", []string{"id"}, func(id *string) []any { return []any{id} },
		sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}))
	require.NoError(t, err)

	for _, tc := range []struct {
		filter string
		limit  int
		want   sqlbase.Total
	}{
		{"priority == 0", sqlbase.DefaultCountLimit, sqlbase.Total{Count: 2, Exact: true}},
		{"", 6, sqlbase.Total{Count: 6, Exact: true}},
		{"", 3, sqlbase.Total{Count: 4}},
		{"priority == 10", 0, sqlbase.Total{Exact: true}},
	} {
		got, err := engine.BoundedCount(t.Context(), listing.Request{Filter: tc.filter}, nil, sqlbase.WithCountLimit(tc.limit))
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%q, limit %d", tc.filter, tc.limit)
	}

	_, err = engine.BoundedCount(t.Context(), listing.Request{}, nil, sqlbase.WithCountProbeTime(time.Second))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

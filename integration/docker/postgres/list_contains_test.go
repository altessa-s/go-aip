// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func TestContainsServerProfiles(t *testing.T) {
	t.Parallel()

	var fixture struct {
		Widgets []listingtest.Widget
		Cases   []struct {
			Filter        string
			Folded, Plain []string
		}
	}

	data, err := os.ReadFile("../../testdata/contains.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixture))

	for _, tc := range []struct {
		name      string
		open      func(testing.TB) *sql.DB
		certified bool
	}{
		{"postgres16", dbtest.Postgres, false}, {"postgres17", dbtest.Postgres17, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := tc.open(t)
			storeWidgets(t, db, "widgets", fixture.Widgets)

			for _, fields := range [][]string{nil, {"name", "score", "tags"}} {
				schema := listingtest.NewSchema(t, listing.WithCaseInsensitiveContainsFields(fields...))
				engine, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "widgets", []string{"id"}, func(id *string) []any { return []any{id} },
					sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}))
				require.NoError(t, err)

				err = engine.CheckSchema(t.Context(), db)
				if len(fields) > 0 && !tc.certified {
					require.ErrorIs(t, err, listing.ErrInvalidSchema)
					continue
				}

				require.NoError(t, err)

				for _, c := range fixture.Cases {
					want := c.Plain
					if len(fields) > 0 {
						want = c.Folded
					}

					req := listing.Request{OrderBy: "id", Filter: c.Filter}
					p, err := engine.List(t.Context(), "widgets", req, nil)
					require.NoError(t, err, c.Filter)
					require.ElementsMatch(t, want, p.Items, c.Filter)
					n, err := engine.Count(t.Context(), req, nil)
					require.NoError(t, err)
					require.Equal(t, int64(len(want)), n, c.Filter)
				}
			}
		})
	}
}

func TestSimpleLowerServerParity(t *testing.T) {
	t.Parallel()
	db := dbtest.Postgres17(t)
	// One scalar maps to one scalar, so concatenation preserves all character boundaries.
	const batchSize = 4096
	for start := rune(1); start <= utf8.MaxRune; start += batchSize {
		var input strings.Builder

		for r := start; r < min(start+batchSize, utf8.MaxRune+1); r++ {
			if utf8.ValidRune(r) {
				input.WriteRune(r)
			}
		}

		var got string

		err := db.QueryRowContext(t.Context(), "SELECT lower($1::text COLLATE pg_catalog.pg_c_utf8)", input.String()).Scan(&got)
		require.NoError(t, err)
		require.Equal(t, evaluator.SimpleLower15_1(input.String()), got, "batch starting at U+%04X", start)
	}
}

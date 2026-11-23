// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

func TestContainsProfiles(t *testing.T) {
	t.Parallel()

	var fixture struct {
		Widgets []listingtest.Widget
		Cases   []struct {
			Filter        string
			Folded, Plain []string
		}
	}

	data, err := os.ReadFile("../testdata/contains.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixture))

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name":  sqlbase.Scalar("name", sqlbase.Text),
		"score": sqlbase.PresenceScalar("score", sqlbase.Text),
		"tags":  sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("name", "score", "tags"), filter.WithArrayFields("tags"))

	for _, mode := range []struct {
		name   string
		fields []string
	}{{"default", nil}, {"folded", []string{"name", "score", "tags"}}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			schema := listingtest.NewSchema(t, listing.WithCaseInsensitiveContainsFields(mode.fields...))
			engine, err := memory.NewListEngine(nil, schema, listingtest.ID, listingtest.Values)
			require.NoError(t, err)
			pg, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true), postgres.WithSchemaCaseInsensitiveContainsFields(mode.fields...))
			require.NoError(t, err)
			ev, err := evaluator.New(policy, evaluator.WithProfile(pg))
			require.NoError(t, err)

			for _, tc := range fixture.Cases {
				want := tc.Plain
				if len(mode.fields) > 0 {
					want = tc.Folded
				}

				p, err := engine.List(t.Context(), "widgets", listing.Request{Filter: tc.Filter, OrderBy: "id"}, fixture.Widgets)
				require.NoError(t, err, tc.Filter)
				require.Equal(t, want, listingtest.IDs(p.Items), tc.Filter)
				program, err := ev.Prepare(filtertest.MustParseFilter(t, tc.Filter))
				require.NoError(t, err)

				ids := []string{}

				for _, w := range fixture.Widgets {
					row := map[string]any{"name": w.Name, "tags": w.Tags}
					if w.Score != nil {
						row["score"] = *w.Score
					}

					ok, err := program.Evaluate(row)
					require.NoError(t, err)

					if ok {
						ids = append(ids, w.ID)
					}
				}

				require.Equal(t, want, ids, tc.Filter)
			}
		})
	}
}

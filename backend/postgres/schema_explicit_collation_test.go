// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

// TestSchemaExplicitCollation keeps the explicit text collation in contract mode, on scalars and array elements.
func TestSchemaExplicitCollation(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text),
		"tags": sqlbase.Array("tags", sqlbase.Text),
		"rank": sqlbase.Scalar("rank", sqlbase.Integer),
	})
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "tags", "rank"))
	require.NoError(t, err)
	p, err := parser.New()
	require.NoError(t, err)

	for _, tc := range []struct {
		filter, plain, collated string
	}{
		{`name > "a"`, `("name" > ($1)::text)`, `(("name" COLLATE "C") > (($1)::text COLLATE "C"))`},
		{`rank > 1`, `("rank" > ($1)::bigint)`, `("rank" > ($1)::bigint)`},
		{`tags.startsWith("a")`, `"tags"`, `COLLATE "C"`},
		{`"a" in tags`, `= ANY("tags")`, `(($1)::text COLLATE "C") = ANY(("tags" COLLATE "C"))`},
	} {
		node, err := p.Parse(context.Background(), tc.filter)
		require.NoError(t, err)

		for explicit, want := range map[bool]string{false: tc.plain, true: tc.collated} {
			sem, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true), postgres.WithSchemaExplicitCollation(explicit))
			require.NoError(t, err)
			tr, err := postgres.NewFilterTranslatorWithSchema(policy, sem)
			require.NoError(t, err)
			sql, _, err := tr.Translate(node)
			require.NoError(t, err, tc.filter)
			require.Contains(t, sql, want, tc.filter)

			if !explicit {
				require.NotContains(t, sql, "COLLATE", tc.filter)
			}
		}
	}
}

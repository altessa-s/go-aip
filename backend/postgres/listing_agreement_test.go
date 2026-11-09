// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// TestPresenceNullsMatchPostgresContract proves that the listing profile, which the memory engine evaluates with, and the
// PostgreSQL contract program, whose SQL the sqlbase listing engine runs, agree on null presence fields, alone and under negation.
func TestPresenceNullsMatchPostgresContract(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
		{Name: "active", Kind: filter.FieldKindBool, Presence: true},
		{Name: "priority", Kind: filter.FieldKindInt},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	require.NoError(t, err)
	memory, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile))
	require.NoError(t, err)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"id": sqlbase.Scalar("id", sqlbase.Text), "rating": sqlbase.PresenceScalar("rating", sqlbase.Integer),
		"score": sqlbase.PresenceScalar("score", sqlbase.Text), "active": sqlbase.PresenceScalar("active", sqlbase.Boolean),
		"priority": sqlbase.Scalar("priority", sqlbase.Integer), "tags": sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	contract, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	sqlProgram, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(contract))
	require.NoError(t, err)

	var rows []map[string]any

	for _, rating := range []any{nil, int64(1), int64(2)} {
		for _, score := range []any{nil, "", "apple", "berry"} {
			for _, active := range []any{nil, true, false} {
				for _, priority := range []int64{0, 4} {
					rows = append(rows, map[string]any{"id": "x", "rating": rating, "score": score, "active": active,
						"priority": priority, "tags": []any{"t1"}})
				}
			}
		}
	}

	for _, expr := range []string{
		`rating == 1`, `rating != 1`, `!(rating == 1)`, `!(rating != 1)`, `rating > 1`, `!(rating > 1)`, `!!(rating > 1)`,
		`rating in [1, 2]`, `!(rating in [1, 2])`, `rating == null`, `rating != null`, `!(rating == null)`,
		`score == "apple"`, `score != ""`, `!(score == "")`, `score.startsWith("a")`, `!score.startsWith("a")`, `!score.contains("e")`,
		`!score.endsWith("y")`, `size(score) == 0`, `!(size(score) > 0)`, `active`, `!active`, `active == true`, `active != true`,
		`!(rating == 1 || priority == 4)`, `!(rating == 1 && score.startsWith("b"))`, `!(!(rating != 2) || active == false)`,
		`rating != 1 && priority == 4`, `!(tags == "t1") || rating != 2`,
		// Three-valued logic: && and || absorb unknown whatever the order of their operands, ! keeps it.
		`rating > 1 || priority == 4`, `priority == 4 || rating > 1`, `rating > 1 && priority == 0`, `priority == 0 && rating > 1`,
		`!(rating > 1 || priority == 0)`, `!(priority == 0 || rating > 1)`, `!(rating > 1 && priority == 4)`,
		`!(size(score) > 0 || priority == 0)`, `active || priority == 4`, `!(active && priority == 4)`,
		`!(!(rating > 1) && priority == 4)`,
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
			require.NoError(t, err)
			want, err := memory.Prepare(q.Filter)
			require.NoError(t, err)
			got, err := sqlProgram.Prepare(q.Filter)
			require.NoError(t, err)

			for _, row := range rows {
				w, err := want.Evaluate(row)
				require.NoError(t, err)
				g, err := got.Evaluate(row)
				require.NoError(t, err)
				require.Equal(t, w, g, "row %v", row)
			}
		})
	}
}

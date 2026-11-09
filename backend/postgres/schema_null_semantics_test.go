// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// nullSchema declares nullable scalar and array columns under their own names.
func nullSchema(t testing.TB, opts ...postgres.SchemaOption) *postgres.Schema {
	t.Helper()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"i": sqlbase.Scalar("i", sqlbase.Integer), "s": sqlbase.Scalar("s", sqlbase.Text), "s2": sqlbase.Scalar("s2", sqlbase.Text),
		"b": sqlbase.Scalar("b", sqlbase.Boolean), "tags": sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(fields, opts...)
	require.NoError(t, err)

	return s
}

// nullRecords returns rows with every combination of NULL, absent and present values.
func nullRecords() []map[string]any {
	var out []map[string]any

	for _, i := range []any{nil, "absent", int64(1), int64(2)} {
		for _, s := range []any{nil, "a", "b", "xa", "жa"} {
			for _, s2 := range []any{nil, "a"} {
				for _, b := range []any{nil, true, false} {
					for _, tags := range []any{nil, []any{}, []any{"a"}, []any{"b", nil}, []any{nil}} {
						row := map[string]any{"s": s, "s2": s2, "b": b, "tags": tags}
						if i != "absent" {
							row["i"] = i
						}

						out = append(out, row)
					}
				}
			}
		}
	}

	return out
}

// nullExpressions holds predicates whose outcome on a NULL operand differs between SQL three-valued logic and the default
// evaluator, alone and under negation.
var nullExpressions = []string{
	`i == 1`, `i != 1`, `!(i == 1)`, `!(i != 1)`, `i > 1`, `!(i > 1)`, `!(i <= 1)`, `1 != i`, `!(1 == i)`,
	`i in [1, 2]`, `!(i in [1, 2])`, `i in [1, null]`, `!(i in [1, null])`, `i in [null]`, `!(i in [null])`, `i in []`, `!(i in [])`,
	`i == null`, `i != null`, `!(i == null)`, `!(i != null)`,
	`s.contains("a")`, `!s.contains("a")`, `s.startsWith("x")`, `!s.startsWith("x")`, `!s.endsWith("a")`, `!s.matches("^a")`,
	`s == s2`, `s != s2`, `!(s == s2)`, `!(s != s2)`, `s.contains(s2)`, `!s.contains(s2)`, `"xa".contains(s2)`, `!"xa".contains(s2)`,
	`s in [s2, "x"]`, `!(s in [s2, "x"])`, `null in [s, "x"]`, `!(null in [s, "x"])`, `null in [null]`, `!(null in [null])`,
	`b`, `!b`, `b == true`, `b != true`, `!(b == false)`,
	`tags == "a"`, `tags != "a"`, `!(tags == "a")`, `!(tags != "a")`, `tags > "a"`, `!(tags > "a")`, `tags <= "a"`, `!(tags <= "a")`,
	`"a" in tags`, `!("a" in tags)`, `tags.contains("a")`, `!tags.contains("a")`, `!tags.startsWith("b")`,
	`size(tags) == 0`, `!(size(tags) == 0)`, `size(s) == 1`, `size(s) != 1`, `!(size(s) == 1)`, `size(s) == null`, `!(size(s) == null)`,
	`size(s) != null`, `size(s) in [1, 2]`, `!(size(s) in [1, 2])`, `!(size(s) in [])`, `i != size(s)`, `!(i == size(s))`,
	`i == 1 && s == "a"`, `!(i == 1 && s == "a")`, `!(i == 1 || s == "a")`, `!(i == 1 || s.contains("x"))`, `!(!(i == 1) && b == true)`,
	`(i == 1) == false`, `(i == 1) != true`, `!((i == 1) == false)`, `(s.contains("a")) != true`, `(i > 1) == false`,
	// Three-valued logic: && and || absorb unknown whatever the order of their operands, ! keeps it.
	`i > 1 || s == "a"`, `s == "a" || i > 1`, `i > 1 && s != "a"`, `s != "a" && i > 1`, `!(i > 1 || s == "a")`,
	`!(s == "a" || i > 1)`, `!(i > 1 && s == "zz")`, `!(!(i > 1))`, `!(i > 1) || b`, `b || i == 1`, `!(b && i == 1)`,
	`size(s) > 0 || i == 1`, `!(size(s) > 0 && i == 1)`, `s.contains(s2) || i == 1`, `!(s.contains(s2) || i == 1)`,
	`!(s.contains(s2) && i == 1)`, `(i > 1 || b) == true`, `!((i > 1 && b) == false)`,
	// substring(): unknown for a NULL text and for a range beyond its end, in every operand position and under negation.
	`s.substring(0, 1) == "x"`, `!(s.substring(0, 1) == "x")`, `s.substring(0, 1) != "x"`, `!(s.substring(0, 1) != "x")`,
	`s.substring(1) == "a"`, `!(s.substring(1) == "a")`, `s.substring(1) < "b"`, `!(s.substring(1) < "b")`, `"a" == s.substring(1)`,
	`s.substring(0) == s2`, `s.substring(0) != s2`, `!(s.substring(0) != s2)`, `s2 != s.substring(1)`, `!(s2 == s.substring(1))`,
	`s.substring(1, 2) in ["a", "x"]`, `!(s.substring(1, 2) in ["a", "x"])`, `s.substring(1) in ["a", null]`,
	`!(s.substring(1) in ["a", null])`, `!(s.substring(1) in [null])`, `s.substring(1) in []`, `!(s.substring(1) in [])`,
	`s.substring(0, 1) == null`, `s.substring(0, 1) != null`, `!(s.substring(0, 1) == null)`, `!(s.substring(0, 1) != null)`,
	`s2 in [s.substring(1), "x"]`, `!(s2 in [s.substring(1), "x"])`, `tags == s.substring(1)`, `!(tags == s.substring(1))`,
	`s.substring(1) in tags`, `!(s.substring(1) in tags)`, `(s.substring(0, 1) == "x") == false`, `s.substring(2) == ""`,
	`!(s.substring(2) == "")`, `s.substring(1, 1) == ""`, `!(s.substring(1, 1) != "")`, `s.substring(0, 2) > s2`,
	`s.substring(0, 1) == s.substring(1)`, `!(s.substring(0, 1) != s2.substring(0, 1))`, `s.substring(1) == "a" || i == 1`,
	`!(s.substring(1) == "a" && i == 1)`, `s.substring(2147483647) == ""`, `!(s.substring(0, 3000000000) == "")`,
	`s.substring(0, 1) == "ж"`, `!(s.substring(1, 2) == "a")`,
}

// TestProgramMatchesDefaultEvaluatorOnNulls proves that the compiled program evaluates NULL and absent values as the default
// evaluator does: a record matches the program exactly where the evaluator returns true. A NULL operand never makes the evaluator
// fail: what both find unknown does not match.
func TestProgramMatchesDefaultEvaluatorOnNulls(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithArrayFields("tags"))
	require.NoError(t, err)
	reference, err := evaluator.New(policy)
	require.NoError(t, err)

	schema := nullSchema(t)
	rows := nullRecords()

	for _, expr := range nullExpressions {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, expr)
			program, err := schema.Compile(policy, node)
			require.NoError(t, err)

			for _, row := range rows {
				want, err := reference.Evaluate(node, row)
				require.NoError(t, err, "row %v", row)
				got, err := program.Evaluate(row, postgres.DefaultMaxDataBudget, nil)
				require.NoError(t, err)
				require.Equal(t, want, got, "row %v", row)
			}
		})
	}
}

// TestProgramProfileMatchesProgram checks the evaluator's profile mode runs the same program.
func TestProgramProfileMatchesProgram(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithArrayFields("tags"))
	require.NoError(t, err)
	schema := nullSchema(t)
	profiled, err := evaluator.New(policy, evaluator.WithProfile(schema))
	require.NoError(t, err)

	for _, expr := range nullExpressions {
		node := filtertest.MustParseFilter(t, expr)
		program, err := schema.Compile(policy, node)
		require.NoError(t, err)
		prepared, err := profiled.Prepare(node)
		require.NoError(t, err)

		for _, row := range nullRecords() {
			want, err := program.Evaluate(row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			got, err := prepared.Evaluate(row)
			require.NoError(t, err)
			require.Equal(t, want, got, "%s on %v", expr, row)
		}
	}
}

// TestNullSQL pins the SQL that gives each predicate the evaluator's NULL semantics: `!` is pushed down to the predicates, a
// nullable column gets an IS NULL disjunct where the evaluator holds for it, and two columns compare null-safely.
func TestNullSQL(t *testing.T) {
	t.Parallel()

	schema := nullSchema(t)
	for _, tc := range []struct{ expr, sql string }{
		{`i != 1`, `("i" <> (1)::bigint OR "i" IS NULL)`},
		{`!(i == 1)`, `("i" <> (1)::bigint OR "i" IS NULL)`},
		{`!(i != 1)`, `("i" = (1)::bigint)`},
		{`!(i > 1)`, `(NOT ("i" > (1)::bigint))`},
		{`!(i in [1, 2])`, `(NOT ("i" = ANY(ARRAY[(1)::bigint, (2)::bigint]::bigint[])) OR ("i") IS NULL)`},
		{`i in [1, null]`, `(("i" = ANY(ARRAY[(1)::bigint, NULL]::bigint[])) OR ("i") IS NULL)`},
		{`!(i in [1, null])`, `(NOT COALESCE(("i" = ANY(ARRAY[(1)::bigint, NULL]::bigint[])), FALSE) AND ("i") IS NOT NULL)`},
		{`!(i in [])`, `TRUE`},
		{`!s.contains("a")`, `(NOT (strpos(("s" COLLATE "C"), ((E'a')::text COLLATE "C")) > 0) OR (("s" COLLATE "C")) IS NULL)`},
		{`s == s2`, `(("s" COLLATE "C") IS NOT DISTINCT FROM ("s2" COLLATE "C"))`},
		{`!(s == s2)`, `(("s" COLLATE "C") IS DISTINCT FROM ("s2" COLLATE "C"))`},
		{`!b`, `(NOT ("b"))`},
		{`!(tags == "a")`, `(NOT COALESCE((((E'a')::text COLLATE "C") = ANY(("tags" COLLATE "C"))), FALSE))`},
		{`!tags.contains("a")`, `(NOT EXISTS (SELECT 1 FROM unnest(("tags" COLLATE "C")) AS "_aip_element_0" WHERE (strpos(("_aip_element_0" COLLATE "C"), ((E'a')::text COLLATE "C")) > 0)))`},
		{`size(tags) == 0`, `(COALESCE(cardinality(("tags" COLLATE "C")), 0) = (0)::bigint)`},
		{`size(s) == null`, `FALSE`},
		{`!(size(s) == null)`, `((length(("s" COLLATE "C"))) IS NOT NULL)`},
		{`i != size(s)`, `("i" <> length(("s" COLLATE "C")) OR ("i" IS NULL AND length(("s" COLLATE "C")) IS NOT NULL))`},
		{`!(i == 1 || s == "a")`, `((("i" <> (1)::bigint OR "i" IS NULL)) AND ((("s" COLLATE "C") <> ((E'a')::text COLLATE "C") OR ("s" COLLATE "C") IS NULL)))`},
		{`(i == 1) == false`, `((CASE WHEN ("i" = (1)::bigint) THEN TRUE WHEN ("i" <> (1)::bigint OR "i" IS NULL) THEN FALSE END) = (false)::boolean)`},
		{`s.substring(0, 2) == "xa"`, `(((CASE WHEN char_length("s") >= 2 THEN substr("s", 1, 2) END) COLLATE "C") = ((E'xa')::text COLLATE "C"))`},
		{`!(s.substring(1) == "a")`, `(((CASE WHEN char_length("s") >= 1 THEN substr("s", 2) END) COLLATE "C") <> ((E'a')::text COLLATE "C"))`},
		{`s.substring(0) < "b"`, `((substr("s", 1) COLLATE "C") < ((E'b')::text COLLATE "C"))`},
		{`s.substring(0) != s2`, `((substr("s", 1) COLLATE "C") <> ("s2" COLLATE "C") OR (("s2" COLLATE "C") IS NULL AND (substr("s", 1) COLLATE "C") IS NOT NULL))`},
		{`s.substring(1, 3) in ["ab"]`, `(((CASE WHEN char_length("s") >= 3 THEN substr("s", 2, 2) END) COLLATE "C") = ANY((ARRAY[((E'ab')::text COLLATE "C")]::text[] COLLATE "C")))`},
		{`!(s.substring(1) in [])`, `((((CASE WHEN char_length("s") >= 1 THEN substr("s", 2) END) COLLATE "C")) IS NOT NULL)`},
		{`s.substring(1) == null`, `FALSE`},
		{`s.substring(1) != null`, `((((CASE WHEN char_length("s") >= 1 THEN substr("s", 2) END) COLLATE "C")) IS NOT NULL)`},
		{`s.substring(2147483647) == ""`, `(((NULL)::text COLLATE "C") = ((E'')::text COLLATE "C"))`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			p := compile(t, schema, tc.expr)
			sql, err := p.Inline()
			require.NoError(t, err)
			require.Equal(t, tc.sql, sql)

			parameterized, args, err := p.SQL()
			require.NoError(t, err)
			require.Len(t, args, countPlaceholders(parameterized))
		})
	}
}

func countPlaceholders(sql string) int {
	n := 0
	for strings.Contains(sql, fmt.Sprintf("$%d)", n+1)) {
		n++
	}

	return n
}

// TestNullSQLShapes pins the remaining shapes: listing contract columns, element tests with a nullable or computed operand
// and list constructors with nullable elements.
func TestNullSQLShapes(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text), "rating": sqlbase.PresenceScalar("rating", sqlbase.Integer),
		"tags": sqlbase.Array("tags", sqlbase.Text), "nums": sqlbase.Array("nums", sqlbase.Integer), "s": sqlbase.Scalar("s", sqlbase.Text),
	})
	require.NoError(t, err)

	plain, err := postgres.NewSchema(fields)
	require.NoError(t, err)
	contract, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)

	for _, tc := range []struct {
		schema   *postgres.Schema
		expr     string
		sql      string
		contract bool
	}{
		{contract, `!(name == null)`, `("name" <> (E'')::text)`, true},
		{contract, `!(tags == null)`, `(cardinality("tags") > 0)`, true},
		{contract, `!(tags in ["a"])`, `(NOT COALESCE(("tags" && ARRAY[(E'a')::text]::text[]), FALSE))`, true},
		{contract, `!(rating in [1])`, `(NOT ("rating" = ANY(ARRAY[(1)::bigint]::bigint[])) OR ("rating") IS NULL)`, true},
		{contract, `rating != size(name)`, `("rating" <> char_length("name") OR ("rating" IS NULL AND char_length("name") IS NOT NULL))`, true},
		{contract, `name.substring(1, 2) == "a"`, `((CASE WHEN char_length("name") >= 2 THEN substr("name", 2, 1) END) = (E'a')::text)`, true},
		{contract, `!(name.substring(0) != s)`, `(substr("name", 1) = "s")`, true},
		{contract, `!(name.substring(1) in ["a", "b"])`, `(NOT ((CASE WHEN char_length("name") >= 1 THEN substr("name", 2) END) = ANY(ARRAY[(E'a')::text, (E'b')::text]::text[])))`, true},
		{contract, `!("a" in tags)`, `(NOT COALESCE(((E'a')::text = ANY("tags")), FALSE))`, true},
		{contract, `!(rating in nums)`, `(NOT COALESCE(("rating" = ANY("nums")), FALSE) AND (("rating") IS NOT NULL OR cardinality("nums") = 0))`, true},
		{plain, `!(nums == size(s))`, `((length(("s" COLLATE "C"))) IS NOT NULL AND NOT COALESCE((length(("s" COLLATE "C")) = ANY("nums")), FALSE))`, false},
		{plain, `!(size(s) in nums)`, `((length(("s" COLLATE "C"))) IS NOT NULL AND NOT COALESCE((length(("s" COLLATE "C")) = ANY("nums")), FALSE))`, false},
		{plain, `!(name in [s, null])`, `(NOT COALESCE((("name" COLLATE "C") = ANY((ARRAY[("s" COLLATE "C"), (NULL COLLATE "C")]::text[] COLLATE "C"))), FALSE) AND (("name" COLLATE "C")) IS NOT NULL)`, false},
		{plain, `!(name in [s])`, `(NOT COALESCE((("name" COLLATE "C") = ANY((ARRAY[("s" COLLATE "C")]::text[] COLLATE "C"))), FALSE) AND ((("name" COLLATE "C")) IS NOT NULL OR (("s" COLLATE "C")) IS NOT NULL))`, false},
		{plain, `!(size(s) in [1, size(name)])`, `(NOT COALESCE((length(("s" COLLATE "C")) = ANY(ARRAY[(1)::bigint, length(("name" COLLATE "C"))]::bigint[])), FALSE) AND (length(("s" COLLATE "C"))) IS NOT NULL AND (length(("name" COLLATE "C"))) IS NOT NULL)`, false},
		{plain, `!(size(s) in [])`, `((length(("s" COLLATE "C"))) IS NOT NULL)`, false},
		{plain, `!tags.contains(s)`, `((("s" COLLATE "C")) IS NOT NULL AND NOT EXISTS (SELECT 1 FROM unnest(("tags" COLLATE "C")) AS "_aip_element_0" WHERE (strpos(("_aip_element_0" COLLATE "C"), ("s" COLLATE "C")) > 0)))`, false},
		{plain, `!"abc".contains(s)`, `(NOT (strpos(((E'abc')::text COLLATE "C"), ("s" COLLATE "C")) > 0))`, false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			policy, err := filter.NewPolicy(filter.WithAnyField(true))
			require.NoError(t, err)

			p, err := tc.schema.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			if tc.sql == "" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			render := p.Inline
			if tc.contract {
				render = p.InlineContract
			}

			sql, err := render()
			require.NoError(t, err)
			require.Equal(t, tc.sql, sql)
		})
	}
}

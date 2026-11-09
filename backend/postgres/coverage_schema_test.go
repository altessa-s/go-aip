// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// covFields declares one field of every SQL kind, as a scalar, a presence column, an array and a JSON path.
func covFields(t testing.TB) *sqlbase.Schema {
	t.Helper()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"b":    sqlbase.Scalar("b", sqlbase.Boolean),
		"i":    sqlbase.Scalar("i", sqlbase.Integer),
		"f":    sqlbase.Scalar("f", sqlbase.Float),
		"s":    sqlbase.Scalar("s", sqlbase.Text),
		"by":   sqlbase.Scalar("by", sqlbase.Bytes),
		"ts":   sqlbase.Scalar("ts", sqlbase.Timestamp),
		"n":    sqlbase.Scalar("n", sqlbase.Numeric),
		"j":    sqlbase.Scalar("j", sqlbase.JSON),
		"u":    sqlbase.Scalar("u", sqlbase.UUID),
		"q.c":  sqlbase.Scalar("q.c", sqlbase.Text),
		"ps":   sqlbase.PresenceScalar("ps", sqlbase.Text),
		"pb":   sqlbase.PresenceScalar("pb", sqlbase.Bytes),
		"pi":   sqlbase.PresenceScalar("pi", sqlbase.Integer),
		"ai":   sqlbase.Array("ai", sqlbase.Integer),
		"af":   sqlbase.Array("af", sqlbase.Float),
		"tx":   sqlbase.Array("tx", sqlbase.Text),
		"an":   sqlbase.Array("an", sqlbase.Numeric),
		"ab":   sqlbase.Array("ab", sqlbase.Bytes),
		"au":   sqlbase.Array("au", sqlbase.UUID),
		"at":   sqlbase.Array("at", sqlbase.Timestamp),
		"abo":  sqlbase.Array("abo", sqlbase.Boolean),
		"aj":   sqlbase.Array("aj", sqlbase.JSON),
		"jp.i": sqlbase.JSONPath("doc", sqlbase.Integer, "i"),
		"jp.b": sqlbase.JSONPath("doc", sqlbase.Boolean, "b"),
		"jp.y": sqlbase.JSONPath("doc", sqlbase.Bytes, "y"),
		"jp.t": sqlbase.JSONPath("doc", sqlbase.Timestamp, "t"),
		"jp.s": sqlbase.JSONPath("doc", sqlbase.Text, "s"),
		"jp.f": sqlbase.JSONPath("doc", sqlbase.Float, "f"),
		"jp.n": sqlbase.JSONPath("doc", sqlbase.Numeric, "n"),
		"jp.a": sqlbase.JSONPath("doc", sqlbase.Text, "a", "-1"),
		"jp.j": sqlbase.JSONPath("doc", sqlbase.JSON, "j"),
		"jp.o": sqlbase.JSONPath("doc", sqlbase.Text, "o", "x"),
	})
	require.NoError(t, err)

	return fields
}

func covSchema(t testing.TB, opts ...postgres.SchemaOption) *postgres.Schema {
	t.Helper()

	s, err := postgres.NewSchema(covFields(t), opts...)
	require.NoError(t, err)

	return s
}

func covPolicy(t testing.TB, opts ...filter.PolicyOption) *filter.Policy {
	t.Helper()

	return filtertest.MustPolicy(t, append([]filter.PolicyOption{filter.WithAnyField(true)}, opts...)...)
}

func covCompile(t testing.TB, s *postgres.Schema, node filter.Node, contract bool) *postgres.Program {
	t.Helper()

	var (
		p   *postgres.Program
		err error
	)
	if contract {
		p, err = s.CompileContract(covPolicy(t), node)
	} else {
		p, err = s.Compile(covPolicy(t), node)
	}

	require.NoError(t, err)

	return p
}

func ident(name string) *filter.IdentNode {
	return &filter.IdentNode{Name: name}
}

func lit(v any) *filter.LiteralNode {
	return &filter.LiteralNode{Value: v}
}

func bin(op filter.Operator, l, r filter.Node) *filter.BinaryOpNode {
	return &filter.BinaryOpNode{Op: op, Left: l, Right: r}
}

func list(elements ...filter.Node) *filter.ListNode {
	return &filter.ListNode{Elements: elements}
}

func not(n filter.Node) *filter.UnaryOpNode {
	return &filter.UnaryOpNode{Op: filter.OpNot, Operand: n}
}

func call(op filter.Operator, target filter.Node, args ...filter.Node) *filter.CallNode {
	return &filter.CallNode{Op: op, Target: target, Args: args}
}

// TestCoverageRenderAllForms renders every operand shape in the four SQL forms, in the default and the contract program, and
// each form of a negation as well.
func TestCoverageRenderAllForms(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)

	for _, tc := range []struct {
		name string
		node filter.Node
		want []string // substrings of the default parameterized SQL
	}{
		{"bool column", ident("b"), []string{`"b"`}},
		{"bool literal", bin(filter.OpAnd, lit(true), ident("b")), []string{"::boolean"}},
		{"int compare", bin(filter.OpGT, ident("i"), lit(int64(3))), []string{`("i" > ($1)::bigint)`}},
		{"int lte", bin(filter.OpLTE, ident("i"), lit(int64(3))), []string{"<="}},
		{"int lt", bin(filter.OpLT, ident("i"), lit(int64(3))), []string{"<"}},
		{"int gte", bin(filter.OpGTE, ident("i"), lit(int64(3))), []string{">="}},
		{"float compare", bin(filter.OpEqual, ident("f"), lit(1.5)), []string{"double precision"}},
		{"float int literal", bin(filter.OpEqual, ident("f"), lit(int64(2))), []string{"double precision"}},
		{"text compare", bin(filter.OpNotEqual, ident("s"), lit("x")), []string{`COLLATE "C"`, `IS NULL`}},
		{"qualified column", bin(filter.OpEqual, ident("q.c"), lit("x")), []string{`"q"."c"`}},
		{"bytes compare", bin(filter.OpEqual, ident("by"), lit([]byte{1, 2})), []string{"::bytea"}},
		{"timestamp compare", bin(filter.OpGT, ident("ts"), lit(ts)), []string{"::timestamptz"}},
		{"numeric compare", bin(filter.OpEqual, ident("n"), lit("1.5")), []string{"::numeric"}},
		{"uuid compare", bin(filter.OpEqual, ident("u"), lit("123e4567-e89b-12d3-a456-426614174000")), []string{"::uuid"}},
		{"json equal", bin(filter.OpEqual, ident("j"), lit(`{"a":1}`)), []string{"::jsonb"}},
		{"json path int", bin(filter.OpEqual, ident("jp.i"), lit(int64(1))), []string{`#>>`, "::bigint"}},
		{"json path json", bin(filter.OpEqual, ident("jp.j"), lit(`[1]`)), []string{`#>`}},
		{"field vs field", bin(filter.OpEqual, ident("s"), ident("ps")), []string{"IS NOT DISTINCT FROM"}},
		{"field ne field", bin(filter.OpNotEqual, ident("s"), ident("ps")), []string{"IS DISTINCT FROM"}},
		{"computed ne field", bin(filter.OpNotEqual, call(filter.OpSize, ident("s")), ident("i")), []string{"IS NOT NULL"}},
		{"size array", bin(filter.OpEqual, call(filter.OpSize, ident("ai")), lit(int64(0))), []string{"cardinality"}},
		{"size json", bin(filter.OpEqual, call(filter.OpSize, ident("j")), lit(int64(0))), []string{"jsonb_array_length"}},
		{"size bytes", bin(filter.OpEqual, call(filter.OpSize, ident("by")), lit(int64(0))), []string{"octet_length"}},
		{"size presence bytes", bin(filter.OpEqual, call(filter.OpSize, ident("pb")), lit(int64(0))), []string{"octet_length"}},
		{"size text", bin(filter.OpEqual, call(filter.OpSize, ident("s")), lit(int64(0))), []string{"length("}},
		{"size presence text", bin(filter.OpEqual, call(filter.OpSize, ident("ps")), lit(int64(0))), []string{"length("}},
		{"size list", bin(filter.OpEqual, call(filter.OpSize, list(lit("a"))), lit(int64(1))), []string{"cardinality((ARRAY["}},
		{"has scalar", call(filter.OpHas, ident("i")), []string{"IS NOT NULL"}},
		{"has array", call(filter.OpHas, ident("ai")), []string{"IS NOT NULL"}},
		{"has uuid", call(filter.OpHas, ident("u")), []string{"IS NOT NULL"}},
		{"has presence", call(filter.OpHas, ident("ps")), []string{"IS NOT NULL"}},
		{"exists", call(filter.OpExists, ident("s")), []string{"IS NOT NULL"}},
		{"contains", call(filter.OpContains, ident("s"), lit("a")), []string{"strpos"}},
		{"starts", call(filter.OpStartsWith, ident("s"), lit("a")), []string{"starts_with"}},
		{"ends", call(filter.OpEndsWith, ident("s"), lit("a")), []string{"right("}},
		{"matches", call(filter.OpMatches, ident("s"), lit("^a")), []string{" ~ "}},
		{"matches insensitive", call(filter.OpMatches, ident("s"), lit("(?i)a")), []string{" ~ "}},
		{"array contains", call(filter.OpContains, ident("tx"), lit("a")), []string{"EXISTS (SELECT 1 FROM unnest"}},
		{"array matches", call(filter.OpMatches, ident("tx"), lit("(?i)a")), []string{"unnest"}},
		{"array contains field", call(filter.OpContains, ident("tx"), ident("s")), []string{"unnest"}},
		{"json contains", call(filter.OpContains, ident("j"), lit(`{"a":[1]}`)), []string{"@>"}},
		{"json contains null", call(filter.OpContains, ident("j"), lit(nil)), []string{"@>"}},
		{"array eq scalar", bin(filter.OpEqual, ident("ai"), lit(int64(1))), []string{"= ANY("}},
		{"array ne scalar", bin(filter.OpNotEqual, ident("ai"), lit(int64(1))), []string{"ANY("}},
		{"array lt scalar", bin(filter.OpLT, ident("ai"), lit(int64(1))), []string{"> ANY("}},
		{"array lte scalar", bin(filter.OpLTE, ident("ai"), lit(int64(1))), []string{">= ANY("}},
		{"array gt scalar", bin(filter.OpGT, ident("ai"), lit(int64(1))), []string{"< ANY("}},
		{"array gte scalar", bin(filter.OpGTE, ident("ai"), lit(int64(1))), []string{"<= ANY("}},
		{"array ne field", bin(filter.OpNotEqual, ident("ai"), ident("i")), []string{"ANY("}},
		{"array ne computed", bin(filter.OpNotEqual, ident("ai"), call(filter.OpSize, ident("s"))), []string{"ANY("}},
		{"array ne presence", bin(filter.OpNotEqual, ident("ai"), ident("pi")), []string{"ANY("}},
		{"array eq list", bin(filter.OpEqual, ident("ai"), list(lit(int64(1)), lit(int64(2)))), []string{"ARRAY["}},
		{"array lt list", bin(filter.OpLT, ident("ai"), list(lit(int64(1)))), []string{"ARRAY["}},
		{"array mixed kinds", bin(filter.OpEqual, ident("ai"), ident("an")), []string{"::numeric[]"}},
		{"array mixed kinds reversed", bin(filter.OpEqual, ident("an"), ident("ai")), []string{"::numeric[]"}},
		{"array float list", bin(filter.OpEqual, ident("ai"), list(lit(1.5))), []string{"double precision"}},
		{"nested list", bin(filter.OpEqual, list(list(lit(int64(1))), list(lit(int64(2)))), list(list(lit(int64(1))), list(lit(int64(2))))), []string{"ARRAY[ARRAY["}},
		{"null literal list", bin(filter.OpEqual, ident("tx"), list(lit(nil))), []string{"text[]"}},
		{"eq null", bin(filter.OpEqual, ident("s"), lit(nil)), []string{"IS NULL"}},
		{"null eq", bin(filter.OpEqual, lit(nil), ident("s")), []string{"IS NULL"}},
		{"ne null", bin(filter.OpNotEqual, ident("s"), lit(nil)), []string{"IS NOT NULL"}},
		{"computed eq null", bin(filter.OpEqual, call(filter.OpSize, ident("s")), lit(nil)), []string{"FALSE"}},
		{"array eq null", bin(filter.OpEqual, ident("ai"), lit(nil)), []string{"IS NULL"}},
		{"uuid eq null", bin(filter.OpEqual, ident("u"), lit(nil)), []string{"IS NULL"}},
		{"in list", bin(filter.OpIn, ident("i"), list(lit(int64(1)), lit(int64(2)))), []string{"= ANY(ARRAY["}},
		{"in empty", bin(filter.OpIn, ident("i"), list()), []string{"FALSE"}},
		{"computed in empty", bin(filter.OpIn, call(filter.OpSize, ident("s")), list()), []string{"FALSE"}},
		{"in null literal", bin(filter.OpIn, ident("i"), list(lit(int64(1)), lit(nil))), []string{"IS NULL"}},
		{"in null column", bin(filter.OpIn, ident("i"), list(lit(int64(1)), ident("pi"))), []string{"IS NULL"}},
		{"in computed", bin(filter.OpIn, ident("i"), list(call(filter.OpSize, ident("s")))), []string{"length("}},
		{"computed in list", bin(filter.OpIn, call(filter.OpSize, ident("s")), list(lit(int64(1)), ident("pi"))), []string{"ANY("}},
		{"in array column", bin(filter.OpIn, ident("i"), ident("ai")), []string{"= ANY(\"ai\")"}},
		{"computed in array", bin(filter.OpIn, call(filter.OpSize, ident("s")), ident("ai")), []string{"ANY("}},
		{"null in array", bin(filter.OpIn, lit(nil), ident("ai")), []string{"ANY("}},
		{"bool compare predicate", bin(filter.OpEqual, bin(filter.OpGT, ident("i"), lit(int64(1))), lit(true)), []string{"CASE WHEN"}},
		{"bool compare call", bin(filter.OpEqual, call(filter.OpContains, ident("s"), lit("a")), ident("b")), []string{"CASE WHEN"}},
		{"bool compare not", bin(filter.OpEqual, not(ident("b")), lit(true)), []string{"CASE WHEN"}},
		{"or", bin(filter.OpOr, ident("b"), bin(filter.OpEqual, ident("i"), lit(int64(1)))), []string{") OR ("}},
		{"string literal compare", bin(filter.OpEqual, lit("a"), lit("a")), []string{"::text"}},
		{"bytes literal", bin(filter.OpEqual, lit([]byte("a")), ident("by")), []string{"::bytea"}},
		{"float special", bin(filter.OpLT, ident("f"), lit(math.Inf(1))), nil},
		{"json order", bin(filter.OpEqual, ident("aj"), list(lit(`1`))), []string{"jsonb[]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := covSchema(t, postgres.WithSchemaJSONBStringCompare(strings.Compare))

			for _, node := range []filter.Node{tc.node, not(tc.node)} {
				for _, contract := range []bool{false, true} {
					p, err := s.Compile(covPolicy(t), node)
					if contract {
						p, err = s.CompileContract(covPolicy(t), node)
					}

					require.NoError(t, err)

					sql, _, err := p.SQL()
					require.NoError(t, err)
					require.NotEmpty(t, sql)

					if node == tc.node && !contract {
						for _, want := range tc.want {
							require.Contains(t, sql, want)
						}
					}

					_, _, err = p.SQLContract()
					require.NoError(t, err)
					_, err = p.Inline()
					require.NoError(t, err)
					_, err = p.InlineContract()
					require.NoError(t, err)
				}
			}
		})
	}
}

// TestCoverageEmptyProgram renders and evaluates a nil filter as a constant match.
func TestCoverageEmptyProgram(t *testing.T) {
	t.Parallel()

	p := covCompile(t, covSchema(t), nil, false)
	sql, args, err := p.SQL()
	require.NoError(t, err)
	require.Equal(t, "1 = 1", sql)
	require.Empty(t, args)

	got, err := p.Evaluate(nil, 0, nil)
	require.NoError(t, err)
	require.True(t, got)
}

// TestCoverageInlineLiterals spells every literal kind inline with an explicit cast.
func TestCoverageInlineLiterals(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)

	for _, tc := range []struct {
		name string
		node filter.Node
		want string
	}{
		{"int", bin(filter.OpEqual, ident("i"), lit(int64(-7))), `(-7)::bigint`},
		{"float", bin(filter.OpEqual, ident("f"), lit(1.5)), `(1.5)::double precision`},
		{"nan", bin(filter.OpEqual, ident("f"), lit(math.NaN())), `('NaN')::double precision`},
		{"inf", bin(filter.OpEqual, ident("f"), lit(math.Inf(1))), `('Infinity')::double precision`},
		{"-inf", bin(filter.OpEqual, ident("f"), lit(math.Inf(-1))), `('-Infinity')::double precision`},
		{"bool", bin(filter.OpEqual, ident("b"), lit(false)), `(false)::boolean`},
		{"text", bin(filter.OpEqual, ident("s"), lit(`a\'b`)), `(E'a\\''b')::text`},
		{"bytes", bin(filter.OpEqual, ident("by"), lit([]byte{0xde, 0xad})), `(decode('dead', 'hex'))::bytea`},
		{"timestamp", bin(filter.OpEqual, ident("ts"), lit(ts)), `(E'2026-01-02 03:04:05.000006Z')::timestamptz`},
		{"numeric", bin(filter.OpEqual, ident("n"), lit("1.50")), `(E'1.5')::numeric`},
		{"numeric special", bin(filter.OpEqual, ident("n"), lit("NaN")), `(E'NaN')::numeric`},
		{"json", bin(filter.OpEqual, ident("j"), lit(`{"b":[1.0,"x\n"],"a":null}`)), `(E'{"a": null, "b": [1.0, "x\\n"]}')::jsonb`},
		{"null", bin(filter.OpEqual, list(lit(nil)), ident("tx")), `ARRAY[(NULL COLLATE "C")]::text[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := covCompile(t, covSchema(t), tc.node, false)
			sql, err := p.Inline()
			require.NoError(t, err)
			require.Contains(t, sql, tc.want)
		})
	}
}

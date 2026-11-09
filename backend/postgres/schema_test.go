// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func schema(t testing.TB) *postgres.Schema {
	t.Helper()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"n": sqlbase.Scalar("n", sqlbase.Numeric), "flag": sqlbase.Scalar("flag", sqlbase.Boolean), "text": sqlbase.Scalar("text", sqlbase.Text),
		"tags": sqlbase.Array("tags", sqlbase.Text), "json": sqlbase.Scalar("doc", sqlbase.JSON), "doc.n": sqlbase.JSONPath("doc", sqlbase.Numeric, "n"),
		"doc.v": sqlbase.JSONPath("doc", sqlbase.JSON, "v"),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)

	return s
}
func compile(t testing.TB, s *postgres.Schema, expr string) *postgres.Program {
	t.Helper()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	program, err := s.Compile(policy, filtertest.MustParseFilter(t, expr))
	require.NoError(t, err)

	return program
}
func TestEvaluation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, expr string
		row        map[string]any
		want       bool
	}{
		{"regex dot newline", `text.matches("a.b")`, map[string]any{"text": "a\nb"}, true},
		{"regex class newline", `text.matches("a[^x]b")`, map[string]any{"text": "a\nb"}, true},
		{"unknown not true", `!flag`, map[string]any{"flag": nil}, false},
		{"unknown or true", `flag || true`, map[string]any{"flag": nil}, true},
		{"unknown and false", `!(flag && false)`, map[string]any{"flag": nil}, true},
		{"numeric exact", `n == 1.00000000000000001`, map[string]any{"n": "1.00000000000000001"}, true},
		{"numeric distinct", `n == 1.00000000000000001`, map[string]any{"n": "1"}, false},
		{"beyond Decimal128", `n == "1e1000"`, map[string]any{"n": "1e1000"}, true},
		{"numeric NaN", `n > "Infinity"`, map[string]any{"n": "NaN"}, true},
		{"SQL null", `n == null`, map[string]any{"n": nil}, true},
		{"not in null", `!(n in [1,null])`, map[string]any{"n": "2"}, true},
		{"null not in", `!(n in [1])`, map[string]any{"n": nil}, true},
		{"null not equal", `n != 1`, map[string]any{"n": nil}, true},
		{"null negated equal", `!(n == 1)`, map[string]any{"n": nil}, true},
		{"null negated ordered", `!(n > 1)`, map[string]any{"n": nil}, false},
		{"null negated string", `!text.contains("a")`, map[string]any{"text": nil}, true},
		{"null fields equal", `n == doc.n`, map[string]any{"n": nil, "doc": `{}`}, true},
		{"null in empty", `n in []`, map[string]any{"n": nil}, false},
		{"array equality", `tags == ["a",null]`, map[string]any{"tags": []any{"a", nil}}, true},
		{"array null element", `tags != "a"`, map[string]any{"tags": []any{"b", nil}}, true},
		{"array null", `tags != "a"`, map[string]any{"tags": nil}, true},
		{"array null size", `size(tags) == 0`, map[string]any{"tags": nil}, true},
		{"array empty", `tags != "a"`, map[string]any{"tags": []string{}}, true},
		{"array strings", `tags.contains("a")`, map[string]any{"tags": []any{nil, "bar"}}, true},
		{"JSON missing", `doc.v == null`, map[string]any{"doc": `{}`}, true},
		{"JSON null present", `has(doc.v)`, map[string]any{"doc": `{"v":null}`}, true},
		{"JSON null not SQL null", `doc.v == null`, map[string]any{"doc": `{"v":null}`}, false},
		{"JSON exact", `doc.n == 1.00000000000000001`, map[string]any{"doc": `{"n":1.00000000000000001}`}, true},
		{"JSON containment", `json.contains('{"x":[2]}')`, map[string]any{"doc": `{"x":[1,2,2],"z":true}`}, true},
		{"JSON nested primitive", `json.contains('[1]')`, map[string]any{"doc": `[[1]]`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := compile(t, schema(t), tc.expr)
			got, err := p.Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestContractSizeOfNullPresenceMatchesSQL(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"score": sqlbase.PresenceScalar("score", sqlbase.Text)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`size(score) == 0`, true},
		{`size(score) > 0`, false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			p, err := s.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			got, err := p.Evaluate(map[string]any{"score": nil}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			contractSQL, _, err := p.SQLContract()
			require.NoError(t, err)
			require.Contains(t, contractSQL, `COALESCE(char_length("score"), 0)`)
		})
	}
}

func TestPortableContractIsAppliedByDirectPostgresCompiler(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAllowedFields("name"), filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindString}), filter.WithPortableContract(true))
	require.NoError(t, err)

	for _, expr := range []string{`name in []`, `name in ["a", null]`, `name.matches("[a-z]+")`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			_, err := s.Compile(policy, filtertest.MustParseFilter(t, expr))
			require.Error(t, err)
		})
	}
}

func TestContractEvaluationMatchesSQLForArraysAndZeroValues(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"tags": sqlbase.Array("tags", sqlbase.Text), "note": sqlbase.Scalar("note", sqlbase.Text), "payload": sqlbase.PresenceScalar("payload", sqlbase.Bytes),
	})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		row  map[string]any
		want bool
	}{
		{`tags in ["go"]`, map[string]any{"tags": []string{"go"}}, true},
		{`tags in ["go"]`, map[string]any{"tags": []string{"rust"}}, false},
		{`note == null`, map[string]any{"note": ""}, true},
		{`note != null`, map[string]any{"note": ""}, false},
		{`tags == null`, map[string]any{"tags": []string{}}, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			program, err := s.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			got, err := program.Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			_, _, err = program.SQLContract()
			require.NoError(t, err)
		})
	}

	program, err := s.Compile(policy, &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "note"}})
	require.NoError(t, err)
	got, err := program.Evaluate(map[string]any{"note": ""}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, got)
}

func TestContractArrayNotEqualSQLIsTwoValuedWithNullElements(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"tags": sqlbase.Array("tags", sqlbase.Text)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	p, err := s.Compile(policy, filtertest.MustParseFilter(t, "tags != \"go\""))
	require.NoError(t, err)
	sql, _, err := p.SQLContract()
	require.NoError(t, err)
	require.Contains(t, sql, "NOT COALESCE")
	require.NotContains(t, sql, "NOT ((")
}

func TestContractUUIDZeroComparisonsDoNotCastEmptyString(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"row.id": sqlbase.Scalar("row_id", sqlbase.UUID)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want string
	}{
		{"row.id == null", "FALSE"},
		{"row.id != null", "TRUE"},
		{"has(row.id)", "TRUE"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			p, err := s.Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			sql, _, err := p.SQLContract()
			require.NoError(t, err)
			require.Contains(t, sql, tc.want)
			require.NotContains(t, sql, "::uuid")

			got, err := p.Evaluate(map[string]any{"row_id": "00000000-0000-0000-0000-000000000000"}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want == "TRUE", got)
		})
	}
}

func TestNonContractPresenceBytesSizeRemainsUnknown(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"payload": sqlbase.PresenceScalar("payload", sqlbase.Bytes)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	program := compile(t, s, `size(payload) == 0`)
	got, err := program.Evaluate(map[string]any{"payload": nil}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, got)

	sql, _, err := program.SQL()
	require.NoError(t, err)
	require.NotContains(t, sql, "COALESCE")
}
func TestSQLAndSnapshot(t *testing.T) {
	t.Parallel()
	s := schema(t)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `text == "' OR TRUE --"`)
	p, err := s.Compile(policy, node)
	require.NoError(t, err)
	sql, args, err := p.SQL()
	require.NoError(t, err)
	require.NotContains(t, sql, "OR TRUE")
	require.Equal(t, []any{"' OR TRUE --"}, args)

	binary, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok)

	binary.Right = &filter.LiteralNode{Value: "changed"}
	got, err := p.Evaluate(map[string]any{"text": "' OR TRUE --"}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, got)

	inline, err := p.Inline()
	require.NoError(t, err)
	require.Contains(t, inline, `E''' OR TRUE --'`)
}
func TestValidation(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAllowedFields("text", "n", "tags", "json"), filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true}))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want error
	}{
		{`true || flag`, filter.ErrFieldNotAllowed}, {`n < null`, filter.ErrUnsupportedOperation},
		{`text == 3`, filter.ErrFieldTypeMismatch}, {`text.matches("\\bword")`, filter.ErrUnsupportedOperation},
		{`n == "1e1000000000"`, filter.ErrFieldTypeMismatch}, {`n == "1e-20000"`, filter.ErrFieldTypeMismatch},
		{`json == '"\\u0000"'`, filter.ErrFieldTypeMismatch},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			_, err := schema(t).Compile(policy, filtertest.MustParseFilter(t, tc.expr))
			require.ErrorIs(t, err, tc.want)
		})
	}
}
func TestBudgetAndCycles(t *testing.T) {
	t.Parallel()
	p := compile(t, schema(t), `json == '{}'`)
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, err := p.Evaluate(map[string]any{"doc": cyclic}, postgres.DefaultMaxDataBudget, nil)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
	_, err = p.Evaluate(map[string]any{"doc": strings.Repeat(" ", 100) + "{}"}, 20, nil)
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}
func TestSchemaFreezeAndConfiguration(t *testing.T) {
	t.Parallel()

	fields := map[string]sqlbase.Field{"text": sqlbase.Scalar("text", sqlbase.Text)}
	sFields, err := sqlbase.NewSchema(fields)
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	delete(fields, "text")

	p := compile(t, s, `text == "yes"`)
	got, err := p.Evaluate(map[string]any{"text": "yes"}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, got)

	_, err = sqlbase.NewSchema(map[string]sqlbase.Field{"bad": sqlbase.Scalar("name; DROP TABLE x", sqlbase.Text)})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"text": filter.FieldKindInt}))
	require.NoError(t, err)
	require.ErrorIs(t, s.ValidatePolicy(policy), filter.ErrInvalidOptions)
}

func TestArrayDimensionsAndBounds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, expr string
		value      any
		want       bool
	}{
		{"multidimensional equality", `tags == [["a","b"],["c","d"]]`, [][]string{{"a", "b"}, {"c", "d"}}, true},
		{"dimensions matter", `tags == ["a","b"]`, [][]string{{"a", "b"}}, false},
		{"lower bound matters", `tags == ["a","b"]`, postgres.ArrayData{Elements: []any{"a", "b"}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 0}}, Valid: true}, false},
		{"lower bound ignored by membership", `"a" in tags`, postgres.ArrayData{Elements: []any{"a", "b"}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 0}}, Valid: true}, true},
		{"invalid wrapper is NULL", `tags == null`, postgres.ArrayData{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := compile(t, schema(t), tc.expr)
			match, err := p.Evaluate(map[string]any{"tags": tc.value}, postgres.DefaultMaxDataBudget, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, match)
		})
	}
}

func TestJSONOrderingRequiresExplicitCollation(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `json < '"z"'`)
	_, err = schema(t).Compile(policy, node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"json": sqlbase.Scalar("doc", sqlbase.JSON)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields, postgres.WithSchemaJSONBStringCompare(strings.Compare))
	require.NoError(t, err)
	p, err := s.Compile(policy, node)
	require.NoError(t, err)
	match, err := p.Evaluate(map[string]any{"doc": `"a"`}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, match)
}

type upperDecoder struct{}

func (upperDecoder) Decode(v any) (any, error) {
	if text, ok := v.(string); ok {
		return strings.ToUpper(text), nil
	}

	return v, nil
}
func TestDecoderCanTransformNativeType(t *testing.T) {
	t.Parallel()
	p := compile(t, schema(t), `text == "YES"`)
	match, err := p.Evaluate(map[string]any{"text": "yes"}, postgres.DefaultMaxDataBudget, upperDecoder{})
	require.NoError(t, err)
	require.True(t, match)
}

func TestNativeUUIDValues(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"u": sqlbase.Scalar("u", sqlbase.UUID), "ids": sqlbase.Array("ids", sqlbase.UUID)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)
	p := compile(t, s, `u == "00000000-0000-0000-0000-000000000000" && u in ids`)
	match, err := p.Evaluate(map[string]any{"u": [16]byte{}, "ids": [][16]byte{{}}}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, match)
}

func TestJSONScalarCasts(t *testing.T) {
	t.Parallel()

	sFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"doc.blob": sqlbase.JSONPath("doc", sqlbase.Bytes, "blob"), "doc.flag": sqlbase.JSONPath("doc", sqlbase.Boolean, "flag"), "doc.i": sqlbase.JSONPath("doc", sqlbase.Integer, "i")})
	require.NoError(t, err)
	s, err := postgres.NewSchema(sFields)
	require.NoError(t, err)

	p := compile(t, s, `doc.blob == b"\x00ab" && doc.flag && doc.i == 1`)
	for _, json := range []string{`{"blob":"\\x00 6162","flag":"yes","i":" +1 "}`, `{"blob":"\\000ab","flag":true,"i":1}`} {
		match, err := p.Evaluate(map[string]any{"doc": json}, postgres.DefaultMaxDataBudget, nil)
		require.NoError(t, err)
		require.True(t, match)
	}

	p = compile(t, s, `doc.i == 1`)
	_, err = p.Evaluate(map[string]any{"doc": `{"i":1.00}`}, postgres.DefaultMaxDataBudget, nil)
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}

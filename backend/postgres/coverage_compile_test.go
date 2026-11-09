// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestCoverageCompileErrors rejects every ill-typed or unsupported expression shape before rendering.
func TestCoverageCompileErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		expr     string
		node     filter.Node
		opts     []postgres.SchemaOption
		policy   []filter.PolicyOption
		contract bool
		wantErr  error
	}{
		{name: "missing operand", node: bin(filter.OpEqual, ident("i"), nil), wantErr: filter.ErrInvalidExpression},
		{name: "undeclared field", expr: `zz == 1`, wantErr: filter.ErrInvalidOptions},
		{name: "undeclared field in call", node: call(filter.OpContains, ident("s"), ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "undeclared hint", node: bin(filter.OpEqual, lit(int64(1)), ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "unsupported unary", node: &filter.UnaryOpNode{Op: filter.OpSize, Operand: ident("b")}, wantErr: filter.ErrUnsupportedOperation},
		{name: "not of int", expr: `!i`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "not error", node: not(bin(filter.OpEqual, ident("i"), lit("a"))), wantErr: filter.ErrFieldTypeMismatch},
		{name: "not boolean root", expr: `i`, wantErr: filter.ErrInvalidExpression},
		{name: "literal kind", expr: `i == "a"`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "literal value", expr: `u == "nope"`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "incompatible kinds", node: bin(filter.OpEqual, ident("s"), ident("i")), wantErr: filter.ErrFieldTypeMismatch},
		{name: "list of arrays", node: bin(filter.OpEqual, list(ident("ai")), ident("ai")), wantErr: filter.ErrUnsupportedOperation},
		{name: "list element kinds", expr: `ai == [1, "a"]`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "list element error", node: bin(filter.OpEqual, ident("ai"), list(ident("zz"))), wantErr: filter.ErrInvalidOptions},
		{name: "list ragged", expr: `ai == [[1], [1, 2]]`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "list nested then scalar", expr: `ai == [[1], 2]`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "list dimensions", expr: `ai == [[1]]`, opts: []postgres.SchemaOption{postgres.WithSchemaMaxArrayDimensions(1)}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json ordering", expr: `j < '1'`, wantErr: filter.ErrUnsupportedOperation},
		{name: "and of int", node: bin(filter.OpAnd, ident("b"), bin(filter.OpEqual, ident("ai"), list())), wantErr: nil},
		{name: "and non bool", node: bin(filter.OpAnd, ident("b"), ident("i")), wantErr: filter.ErrFieldTypeMismatch},
		{name: "and error left", node: bin(filter.OpAnd, ident("zz"), ident("b")), wantErr: filter.ErrInvalidOptions},
		{name: "and error right", node: bin(filter.OpAnd, ident("b"), ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "in scalar", node: bin(filter.OpIn, ident("i"), lit(int64(1))), wantErr: filter.ErrFieldTypeMismatch},
		{name: "array in list", expr: `ai in [1]`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "unsupported binary", node: bin(filter.OpContains, ident("i"), lit(int64(1))), wantErr: filter.ErrUnsupportedOperation},
		{name: "ordered null", node: bin(filter.OpGT, ident("i"), lit(nil)), wantErr: filter.ErrUnsupportedOperation},
		{name: "scalar vs array", node: bin(filter.OpEqual, ident("i"), ident("ai")), wantErr: filter.ErrUnsupportedOperation},
		{name: "literal kind policy", expr: `i == 1.5`, policy: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"i": filter.FieldKindInt})}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "size argument", node: call(filter.OpSize, ident("s"), lit("a")), wantErr: filter.ErrFieldTypeMismatch},
		{name: "size of int", expr: `size(i) == 1`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "has of call", node: call(filter.OpHas, call(filter.OpSize, ident("s"))), wantErr: filter.ErrUnsupportedOperation},
		{name: "has argument", node: call(filter.OpHas, ident("s"), lit("a")), wantErr: filter.ErrUnsupportedOperation},
		{name: "unsupported call", node: call(filter.OpIn, ident("s")), wantErr: filter.ErrUnsupportedOperation},
		{name: "substring not boolean", node: call(filter.OpSubstring, ident("s"), lit(int64(1))), wantErr: filter.ErrInvalidExpression},
		{name: "substring of int", expr: `i.substring(0) == "a"`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "substring of json", expr: `j.substring(0) == "a"`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "substring of array", expr: `tx.substring(0) == "a"`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring of literal", expr: `"ab".substring(1) == "b"`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring of substring", node: bin(filter.OpEqual, call(filter.OpSubstring, call(filter.OpSubstring, ident("s"), lit(int64(0))), lit(int64(0))), lit("a")), wantErr: filter.ErrUnsupportedOperation},
		{name: "substring field bound", expr: `s.substring(0, i) == "a"`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring float bound", expr: `s.substring(0, 1.5) == "a"`, wantErr: filter.ErrInvalidExpression},
		{name: "substring null bound", expr: `s.substring(null) == "a"`, wantErr: filter.ErrInvalidExpression},
		{name: "substring negative start", expr: `s.substring(-1) == "a"`, wantErr: filter.ErrInvalidExpression},
		{name: "substring reversed", expr: `s.substring(2, 1) == "a"`, wantErr: filter.ErrInvalidExpression},
		{name: "substring arity", node: bin(filter.OpEqual, call(filter.OpSubstring, ident("s")), lit("a")), wantErr: filter.ErrInvalidExpression},
		{name: "substring three bounds", expr: `s.substring(0, 1, 2) == "a"`, wantErr: filter.ErrInvalidExpression},
		{name: "substring kind", expr: `s.substring(0) == 1`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "substring size", expr: `size(s.substring(1)) == 1`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring string target", expr: `s.substring(1).contains("a")`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring string argument", expr: `s.contains(s.substring(1))`, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring portable", expr: `s.substring(1) == "a"`, policy: []filter.PolicyOption{filter.WithPortableContract(true), filter.WithFieldTypes(map[string]filter.FieldKind{"s": filter.FieldKindString})}, wantErr: filter.ErrUnsupportedOperation},
		{name: "substring json path", expr: `jp.s.substring(1) == "a"`},
		{name: "substring presence contract", expr: `ps.substring(0, 1) in ["a"]`, contract: true},
		{name: "call target error", node: call(filter.OpSize, ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "string call arity", node: call(filter.OpContains, ident("s")), wantErr: filter.ErrFieldTypeMismatch},
		{name: "string call int target", expr: `i.contains("a")`, wantErr: filter.ErrFieldTypeMismatch},
		{name: "string call int argument", node: call(filter.OpContains, ident("s"), lit(int64(1))), wantErr: filter.ErrFieldTypeMismatch},
		{name: "string call array argument", node: call(filter.OpContains, ident("s"), ident("tx")), wantErr: filter.ErrFieldTypeMismatch},
		{name: "string call argument error", node: call(filter.OpContains, ident("s"), ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "json contains array", node: call(filter.OpContains, ident("j"), list(lit(`1`))), wantErr: filter.ErrFieldTypeMismatch},
		{name: "json contains error", node: call(filter.OpContains, ident("j"), ident("zz")), wantErr: filter.ErrInvalidOptions},
		{name: "json contains bad literal", node: call(filter.OpContains, ident("j"), lit(`{`)), wantErr: filter.ErrFieldTypeMismatch},
		{name: "matches field", node: call(filter.OpMatches, ident("s"), ident("ps")), wantErr: filter.ErrUnsupportedOperation},
		{name: "matches invalid", node: call(filter.OpMatches, ident("s"), lit("(")), wantErr: filter.ErrInvalidRegex},
		{name: "portable regex POSIX", node: call(filter.OpMatches, ident("s"), lit(`\d`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, wantErr: filter.ErrUnsupportedOperation},
		{name: "portable regex non-ASCII", node: call(filter.OpMatches, ident("s"), lit(`(?i)é`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, contract: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "portable regex ok", node: call(filter.OpMatches, ident("s"), lit(`(?i)^a.*b$`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, contract: true},
		{name: "portable regex default", node: call(filter.OpMatches, ident("s"), lit(`^a.*b\.c$`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}},
		{name: "portable regex insensitive outside the contract", node: call(filter.OpMatches, ident("s"), lit(`(?i)ab`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}},
		// The PostgreSQL portable rule is filter.CheckPortableRegex, as in MariaDB and ClickHouse, not the POSIX subset.
		{name: "portable regex single dot", node: call(filter.OpMatches, ident("s"), lit(`a.b`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, contract: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "portable regex class", node: call(filter.OpMatches, ident("s"), lit(`[a-z]+`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, wantErr: filter.ErrUnsupportedOperation},
		{name: "portable regex alternation", node: call(filter.OpMatches, ident("s"), lit(`a|b`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, contract: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "portable regex invalid", node: call(filter.OpMatches, ident("s"), lit(`(`)), policy: []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})}, wantErr: filter.ErrInvalidRegex},
		{name: "field not allowed", expr: `i == 1`, policy: []filter.PolicyOption{filter.WithAnyField(false), filter.WithAllowedFields("s")}, wantErr: filter.ErrFieldNotAllowed},
		{name: "portable contract", expr: `s in []`, policy: []filter.PolicyOption{filter.WithPortableContract(true), filter.WithFieldTypes(map[string]filter.FieldKind{"s": filter.FieldKindString})}, wantErr: filter.ErrInvalidExpression},
		{name: "decimal operand", expr: `n == 1.5`, policy: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"n": filter.FieldKindDecimal})}},
		{name: "decimal operand error", node: bin(filter.OpEqual, ident("n"), &filter.LiteralNode{Value: 1.5, Text: "x"}), policy: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"n": filter.FieldKindDecimal})}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "decimal operand error right", node: bin(filter.OpEqual, &filter.LiteralNode{Value: 1.5, Text: "x"}, ident("n")), policy: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"n": filter.FieldKindDecimal})}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "huge numeric literal", node: bin(filter.OpEqual, ident("n"), lit(decimal.New(1, 200000))), wantErr: filter.ErrFieldTypeMismatch},
		{name: "literal budget", expr: `s == "abcdefghijklmnopqrstuvwxyz"`, opts: []postgres.SchemaOption{postgres.WithSchemaMaxDataBudget(4)}, wantErr: filter.ErrDataLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := tc.node
			if node == nil {
				node = filtertest.MustParseFilter(t, tc.expr)
			}

			s := covSchema(t, tc.opts...)

			var err error
			if tc.contract {
				_, err = s.CompileContract(covPolicy(t, tc.policy...), node)
			} else {
				_, err = s.Compile(covPolicy(t, tc.policy...), node)
			}

			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestCoverageNilSchemaAndPolicy reports a missing schema or policy as invalid options, never a panic.
func TestCoverageNilSchemaAndPolicy(t *testing.T) {
	t.Parallel()

	var nilSchema *postgres.Schema

	require.False(t, nilSchema.ContractMode())
	require.ErrorIs(t, nilSchema.ValidatePolicy(covPolicy(t)), filter.ErrInvalidOptions)
	_, err := nilSchema.Compile(covPolicy(t), filtertest.MustParseFilter(t, `i == 1`))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)

	s := covSchema(t)
	_, err = s.Compile(nil, filtertest.MustParseFilter(t, `i == 1`))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	_, err = s.CompileEvaluator(covPolicy(t), filtertest.MustParseFilter(t, `i == 1`))
	require.NoError(t, err)
	_, _, err = s.Lookup(nil, covRow{}, "i", postgres.DefaultMaxDataBudget, nil)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

// TestCoverageNewSchemaOptions validates the PostgreSQL semantic options.
func TestCoverageNewSchemaOptions(t *testing.T) {
	t.Parallel()

	empty, collation, prefix := "", "en_US", "custom_alias"

	for _, tc := range []struct {
		name    string
		opts    []postgres.SchemaOption
		wantErr bool
	}{
		{"defaults", nil, false},
		{"collation without compare", []postgres.SchemaOption{postgres.WithSchemaTextCollation("en_US")}, true},
		{"collation with compare", []postgres.SchemaOption{postgres.WithSchemaTextCollation(&collation), postgres.WithSchemaTextCompare(strings.Compare)}, false},
		{"collation NUL", []postgres.SchemaOption{postgres.WithSchemaTextCollation("a\x00"), postgres.WithSchemaTextCompare(strings.Compare)}, true},
		{"collation empty ignored", []postgres.SchemaOption{postgres.WithSchemaTextCollation(""), postgres.WithSchemaTextCollation(&empty), postgres.WithSchemaTextCollation((*string)(nil))}, false},
		{"nil compare", []postgres.SchemaOption{postgres.WithSchemaTextCompare(nil)}, true},
		{"alias prefix", []postgres.SchemaOption{postgres.WithSchemaArrayAliasPrefix(&prefix)}, false},
		{"alias prefix ignored", []postgres.SchemaOption{postgres.WithSchemaArrayAliasPrefix(" "), postgres.WithSchemaArrayAliasPrefix((*string)(nil))}, false},
		{"alias prefix invalid", []postgres.SchemaOption{postgres.WithSchemaArrayAliasPrefix("1x")}, true},
		{"negative precision", []postgres.SchemaOption{postgres.WithSchemaTimestampPrecision(-time.Second)}, true},
		{"zero precision", []postgres.SchemaOption{postgres.WithSchemaTimestampPrecision(0)}, false},
		{"nonpositive limits ignored", []postgres.SchemaOption{postgres.WithSchemaMaxDataBudget(0), postgres.WithSchemaMaxValueDepth(0), postgres.WithSchemaMaxArrayDimensions(-1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := postgres.NewSchema(covFields(t), tc.opts...)
			if tc.wantErr {
				require.ErrorIs(t, err, filter.ErrInvalidOptions)
				return
			}

			require.NoError(t, err)
		})
	}

	_, err := postgres.NewSchema(nil)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

// TestCoverageCollationAndAliases quotes a custom collation and picks an array alias that no column of the expression uses.
func TestCoverageCollationAndAliases(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"s":    sqlbase.Scalar("s", sqlbase.Text),
		"tags": sqlbase.Array("tags", sqlbase.Text),
		"x":    sqlbase.Scalar("x._aip_element_0", sqlbase.Text),
		"y":    sqlbase.Scalar("_aip_element_1", sqlbase.Text),
	})
	require.NoError(t, err)

	s, err := postgres.NewSchema(fields, postgres.WithSchemaTextCollation(`en"US`), postgres.WithSchemaTextCompare(strings.Compare))
	require.NoError(t, err)

	node := filtertest.MustParseFilter(t, `x == y && tags.contains("a") && tags.contains("b")`)
	p := covCompile(t, s, node, false)
	sql, _, err := p.SQL()
	require.NoError(t, err)
	require.Contains(t, sql, `COLLATE "en""US"`)
	require.Contains(t, sql, `AS "_aip_element_2"`)
	require.Contains(t, sql, `AS "_aip_element_3"`)

	custom, err := postgres.NewSchema(fields, postgres.WithSchemaArrayAliasPrefix("el"))
	require.NoError(t, err)
	sql, _, err = covCompile(t, custom, node, false).SQL()
	require.NoError(t, err)
	require.Contains(t, sql, `AS "el0"`)
	require.Contains(t, sql, `AS "el1"`)
}

// TestCoverageContractPatterns renders matches() under the listing contract with the (?p) option, ~* for a leading (?i), and
// a negated array predicate with a field needle.
func TestCoverageContractPatterns(t *testing.T) {
	t.Parallel()

	s := covSchema(t)

	for _, tc := range []struct {
		name string
		node filter.Node
		want []string
	}{
		{"insensitive", call(filter.OpMatches, ident("s"), lit("(?i)ab")), []string{"~*", "(?p)ab"}},
		{"sensitive", call(filter.OpMatches, ident("s"), lit("ab")), []string{" ~ ", "(?p)ab"}},
		{"array", call(filter.OpMatches, ident("tx"), lit("ab")), []string{"unnest", "(?p)ab"}},
		{"negated presence", not(call(filter.OpContains, ident("ps"), lit("a"))), []string{"IS NULL"}},
		{"negated array field needle", not(call(filter.OpContains, ident("tx"), ident("s"))), []string{"IS NOT NULL AND NOT EXISTS"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := covCompile(t, s, tc.node, true)
			sql, args, err := p.SQLContract()
			require.NoError(t, err)

			inline, err := p.InlineContract()
			require.NoError(t, err)

			for _, want := range tc.want {
				require.Contains(t, sql+" "+inline+" "+strings.Join(stringArgs(args), " "), want)
			}
		})
	}
}

func stringArgs(args []any) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// TestCoverageLookup reads declared fields into their public representation.
func TestCoverageLookup(t *testing.T) {
	t.Parallel()

	s := covSchema(t)
	policy := covPolicy(t)

	for _, tc := range []struct {
		name        string
		field       string
		row         covRow
		want        any
		wantPresent bool
		wantErr     error
	}{
		{"numeric", "n", covRow{"n": "1.5"}, decimal.RequireFromString("1.5"), true, nil},
		{"numeric NaN", "n", covRow{"n": "NaN"}, "NaN", true, nil},
		{"numeric infinity", "n", covRow{"n": "Infinity"}, "Infinity", true, nil},
		{"numeric minus infinity", "n", covRow{"n": "-Infinity"}, "-Infinity", true, nil},
		{"array", "an", covRow{"an": []string{"1"}}, postgres.ArrayData{Elements: []any{decimal.RequireFromString("1")}, Dimensions: []postgres.Dimension{{Length: 1, LowerBound: 1}}, Valid: true}, true, nil},
		{"json", "j", covRow{"j": `{"a": [1, null]}`}, map[string]any{"a": []any{decimal.RequireFromString("1"), postgres.JSONNull{}}}, true, nil},
		{"missing", "i", covRow{}, nil, false, nil},
		{"null", "i", covRow{"i": nil}, nil, true, nil},
		{"text", "s", covRow{"s": "a"}, "a", true, nil},
		{"bad value", "i", covRow{"i": "x"}, nil, false, filter.ErrFieldTypeMismatch},
		{"undeclared", "zz", covRow{}, nil, false, filter.ErrInvalidOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, present, err := s.Lookup(policy, tc.row, tc.field, postgres.DefaultMaxDataBudget, nil)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantPresent, present)

			if d, ok := tc.want.(decimal.Decimal); ok {
				require.True(t, d.Equal(got.(decimal.Decimal)))
				return
			}

			if tc.name == "array" || tc.name == "json" {
				require.Equal(t, normalizeDecimals(tc.want), normalizeDecimals(got))
				return
			}

			require.Equal(t, tc.want, got)
		})
	}

	_, _, err := s.Lookup(covPolicy(t, filter.WithAnyField(false), filter.WithAllowedFields("s")), covRow{}, "i", postgres.DefaultMaxDataBudget, nil)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

// normalizeDecimals replaces decimals with their canonical text so that structurally equal values compare equal.
func normalizeDecimals(v any) any {
	switch x := v.(type) {
	case decimal.Decimal:
		return x.String()
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeDecimals(item)
		}

		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalizeDecimals(item)
		}

		return out
	case postgres.ArrayData:
		x.Elements, _ = normalizeDecimals(x.Elements).([]any)
		return x
	default:
		return v
	}
}

// TestCoverageJSONDefaultStringOrder compares JSON strings bytewise when no comparator is configured, which only equality uses.
func TestCoverageJSONDefaultStringOrder(t *testing.T) {
	t.Parallel()

	p := covCompile(t, covSchema(t), filtertest.MustParseFilter(t, `j == '{"a": "x"}'`), false)
	got, err := p.Evaluate(covRow{"j": `{"a": "y"}`}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, got)

	got, err = p.Evaluate(covRow{"j": `{"b": "x"}`}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, got)

	got, err = p.Evaluate(covRow{"j": math.Pi}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.False(t, got)
}

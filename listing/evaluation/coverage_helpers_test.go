// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// coverageFields declares one field per kind and shape the profile distinguishes.
func coverageFields() []listing.Field {
	return []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "flag", Kind: filter.FieldKindBool},
		{Name: "count", Kind: filter.FieldKindInt},
		{Name: "score", Kind: filter.FieldKindFloat},
		{Name: "price", Kind: filter.FieldKindDecimal},
		{Name: "at", Kind: filter.FieldKindTimestamp},
		{Name: "blob", Kind: filter.FieldKindBytes},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
		{Name: "nums", Kind: filter.FieldKindInt, Array: true},
		{Name: "floats", Kind: filter.FieldKindFloat, Array: true},
		{Name: "prices", Kind: filter.FieldKindDecimal, Array: true},
		{Name: "times", Kind: filter.FieldKindTimestamp, Array: true},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "opt", Kind: filter.FieldKindInt, Optional: true},
		{Name: "optFloat", Kind: filter.FieldKindFloat, Optional: true},
		{Name: "optBool", Kind: filter.FieldKindBool, Optional: true},
		{Name: "optBlob", Kind: filter.FieldKindBytes, Optional: true},
		{Name: "optAt", Kind: filter.FieldKindTimestamp, Optional: true},
		{Name: "optPrice", Kind: filter.FieldKindDecimal, Optional: true},
		{
			Name: "status", Kind: filter.FieldKindString, Optional: true, EnumZero: "STATUS_UNSPECIFIED",
			Enum: map[string]any{"STATUS_UNSPECIFIED": "", "ACTIVE": "active", "INACTIVE": "inactive"},
		},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "label", Kind: filter.FieldKindString, Presence: true},
		{Name: "enabled", Kind: filter.FieldKindBool, Presence: true},
		{Name: "cost", Kind: filter.FieldKindDecimal, Presence: true},
		{Name: "pblob", Kind: filter.FieldKindBytes, Presence: true},
	}
}

// coverageSchema builds the shared schema with every builtin string function enabled.
func coverageSchema(t *testing.T, opts ...listing.ProfileOption) *listing.Schema {
	t.Helper()

	opts = append([]listing.ProfileOption{
		listing.WithBuiltinFunctions("contains", "startsWith", "endsWith", "matches", "size", "has", "exists", "timestamp"),
	}, opts...)
	schema, err := listing.NewSchema(coverageFields(), []string{"id"}, listing.NewProfile(opts...))
	require.NoError(t, err)

	return schema
}

// anyFieldPolicy validates only the AST shape, so the profile sees operands a schema policy would reject.
func anyFieldPolicy(t *testing.T) *filter.Policy {
	t.Helper()

	policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithArrayFields("tags", "nums", "floats", "prices", "times"))
	require.NoError(t, err)

	return policy
}

type coverageCase struct {
	name    string
	expr    string
	node    filter.Node
	data    map[string]any
	want    bool
	wantErr error
	anyErr  bool
}

// parser turns a case expression into the AST the profile compiles.
type parser func(t *testing.T, expr string) filter.Node

// rawParser parses an expression without schema rewriting.
func rawParser(t *testing.T, expr string) filter.Node {
	t.Helper()

	return filtertest.MustParseFilter(t, expr)
}

// schemaParser compiles an expression through schema, so enum names are rewritten to their stored values.
func schemaParser(schema *listing.Schema) parser {
	return func(t *testing.T, expr string) filter.Node {
		t.Helper()

		query, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		require.NoError(t, err, expr)

		return query.Filter
	}
}

func (tc coverageCase) parse(t *testing.T, parse parser) filter.Node {
	t.Helper()

	if tc.node != nil {
		return tc.node
	}

	return parse(t, tc.expr)
}

// runProgram compiles node directly against profile with policy and evaluates it with the default budget.
func runProgram(t *testing.T, profile evaluator.Compiler, policy *filter.Policy, node filter.Node, data map[string]any) (bool, error) {
	t.Helper()

	program, err := profile.CompileEvaluator(policy, node)
	if err != nil {
		return false, err
	}

	return program.Evaluate(data, evaluator.DefaultMaxDataBudget, nil)
}

// runCases evaluates every case through profile and policy and checks its outcome.
func runCases(t *testing.T, profile evaluator.Compiler, policy *filter.Policy, parse parser, cases []coverageCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			program, err := profile.CompileEvaluator(policy, tc.parse(t, parse))
			require.NoError(t, err)

			got, err := program.Evaluate(tc.data, evaluator.DefaultMaxDataBudget, nil)

			switch {
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			case tc.anyErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

// newCoverageProfile builds a profile over the shared schema with the bounded reader.
func newCoverageProfile(t *testing.T, schema *listing.Schema) *evaluation.Profile {
	t.Helper()

	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	require.NoError(t, err)

	return profile
}

func ident(name string) *filter.IdentNode { return &filter.IdentNode{Name: name} }

func lit(value any) *filter.LiteralNode { return &filter.LiteralNode{Value: value} }

func bin(op filter.Operator, left, right filter.Node) *filter.BinaryOpNode {
	return &filter.BinaryOpNode{Op: op, Left: left, Right: right}
}

func call(op filter.Operator, target filter.Node, args ...filter.Node) *filter.CallNode {
	return &filter.CallNode{Op: op, Target: target, Args: args}
}

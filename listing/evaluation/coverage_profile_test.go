// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

func TestCoverageNewRejectsMissingParts(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)

	for _, tc := range []struct {
		name   string
		schema *listing.Schema
		reader evaluation.FieldReader
	}{
		{name: "nil schema", reader: evaluation.NewReader(nil, 0)},
		{name: "nil reader", schema: schema},
		{name: "typed nil reader", schema: schema, reader: (*evaluation.Reader)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profile, err := evaluation.New(tc.schema, tc.reader)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			require.Nil(t, profile)
		})
	}
}

type identityDecoder struct{}

func (identityDecoder) Decode(v any) (any, error) { return v, nil }

func TestCoverageProgramEvaluate(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	// A reader returning stored values unchanged leaves pointer normalization to the profile.
	plain, err := evaluation.New(schema, mapReader{})
	require.NoError(t, err)

	policy := schema.FilterPolicy()
	parse := schemaParser(schema)
	record := coverageRecord(nil)
	long := coverageRecord(map[string]any{"name": strings.Repeat("a", 64), "tags": []string{"a", "b", "c", "d", "e", "f"}})

	require.NoError(t, profile.ValidatePolicy(policy))

	for _, tc := range []struct {
		name    string
		plain   bool
		node    filter.Node
		data    map[string]any
		maxWork int
		decoder evaluator.ValueDecoder
		want    bool
		wantErr error
	}{
		{name: "empty filter matches", node: nil, data: record, maxWork: 1, want: true},
		{name: "decoder refused", node: parse(t, `name == "alpha"`), data: record, maxWork: 100, decoder: identityDecoder{}, wantErr: filter.ErrInvalidOptions},
		{name: "no work budget", node: parse(t, `name == "alpha"`), data: record, maxWork: 0, wantErr: filter.ErrDataLimitExceeded},
		{name: "budget exhausted in right operand", node: parse(t, `flag && name == "alpha"`), data: record, maxWork: 2, wantErr: filter.ErrDataLimitExceeded},
		{name: "string function charges text", node: parse(t, `name.contains("b")`), data: long, maxWork: 20, wantErr: filter.ErrDataLimitExceeded},
		{name: "array read charged", node: parse(t, `tags == "z"`), data: long, maxWork: 6, wantErr: filter.ErrDataLimitExceeded},
		{name: "array elements charged", node: parse(t, `tags == "z"`), data: long, maxWork: 8, wantErr: filter.ErrDataLimitExceeded},
		{name: "array normalization charged", node: parse(t, `"z" in tags`), data: long, maxWork: 3, wantErr: filter.ErrDataLimitExceeded},
		{name: "pointer normalization charged", plain: true, node: parse(t, `count == 5`), data: coverageRecord(map[string]any{"count": new(new(new(int64(5))))}), maxWork: 2, wantErr: filter.ErrDataLimitExceeded},
		{name: "array pointer normalization charged", plain: true, node: parse(t, `tags == "a"`), data: coverageRecord(map[string]any{"tags": new([]*string{new("a")})}), maxWork: 3, wantErr: filter.ErrDataLimitExceeded},
		{name: "ample budget", node: parse(t, `name.contains("a") && tags == "f"`), data: long, maxWork: 1000, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiler := profile
			if tc.plain {
				compiler = plain
			}

			program, err := compiler.CompileEvaluator(policy, tc.node)
			require.NoError(t, err)

			got, err := program.Evaluate(tc.data, tc.maxWork, tc.decoder)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.False(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCoverageCompileEvaluatorErrors(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)

	_, err := profile.CompileEvaluator(nil, filter.Node(nil))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)

	_, err = profile.CompileEvaluator(schema.FilterPolicy(), bin(filter.OpEqual, ident("undeclared"), lit("x")))
	require.Error(t, err)

	errCompile := errors.New("regex refused")
	refusing, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithUnsafeRegex(true),
		filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { return nil, errCompile }))
	require.NoError(t, err)

	_, err = profile.CompileEvaluator(refusing, rawParser(t, `name.matches("a")`))
	require.ErrorIs(t, err, errCompile)
}

// TestCoveragePrepareMatcherError fails the compiler only on the call the profile makes after validation.
func TestCoveragePrepareMatcherError(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	errCompile := errors.New("regex refused late")

	for _, expr := range []string{`name.matches("a")`, `flag && name.matches("a")`, `flag && (flag || name.matches("a"))`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			newPolicy := func(failAt int) (*filter.Policy, *int) {
				calls := 0
				policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithUnsafeRegex(true),
					filter.WithRegexCompiler(func(pattern string) (filter.RegexMatcher, error) {
						calls++
						if calls == failAt {
							return nil, errCompile
						}

						return regexp.Compile(pattern)
					}))
				require.NoError(t, err)

				return policy, &calls
			}

			counting, calls := newPolicy(0)
			_, err := profile.CompileEvaluator(counting, rawParser(t, expr))
			require.NoError(t, err)

			failing, _ := newPolicy(*calls)
			_, err = profile.CompileEvaluator(failing, rawParser(t, expr))
			require.ErrorIs(t, err, errCompile)
		})
	}
}

func TestCoverageMatchersPreparedPerPattern(t *testing.T) {
	t.Parallel()

	calls := 0
	policy, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithUnsafeRegex(true),
		filter.WithRegexCompiler(func(pattern string) (filter.RegexMatcher, error) {
			calls++
			return regexp.Compile(pattern)
		}))
	require.NoError(t, err)

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	program, err := profile.CompileEvaluator(policy, rawParser(t, `name.matches("^al") && (label.matches("^al") || id.matches("a"))`))
	require.NoError(t, err)

	compiled := calls

	for range 2 {
		got, err := program.Evaluate(coverageRecord(nil), evaluator.DefaultMaxDataBudget, nil)
		require.NoError(t, err)
		require.True(t, got)
	}

	require.Equal(t, compiled, calls)
}

func TestCoverageBindBudgetWithPlainReader(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile, err := evaluation.New(schema, mapReader{})
	require.NoError(t, err)

	budget := &countingBudget{}
	bound := profile.BindBudget(budget)

	compiler, ok := bound.(evaluator.Compiler)
	require.True(t, ok)

	got, err := runProgram(t, compiler, schema.FilterPolicy(), schemaParser(schema)(t, `name == "alpha"`), coverageRecord(nil))
	require.NoError(t, err)
	require.True(t, got)
	// Only predicate work is charged: the plain reader does not bind the budget.
	require.Positive(t, budget.used)
}

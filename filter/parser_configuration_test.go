// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"
	"time"

	"cel.dev/cel-go/cel"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/filter/parser"
)

func TestParserInstanceConfiguration(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithTimestampParser(func(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }), parser.WithOrderedEnums(true), parser.WithEnumNames(map[string]map[string]any{"rank": {"HIGH": uint64(1) << 63}}))
	require.NoError(t, err)
	_, err = p.Parse(t.Context(), `created > timestamp("2026-01-01") && rank >= "HIGH"`)
	require.NoError(t, err)
	independent, err := parser.New()
	require.NoError(t, err)
	_, err = independent.Parse(t.Context(), `created > timestamp("2026-01-01")`)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

// refusingProfile is a compiled profile that refuses every filter.
type refusingProfile struct{}

func (refusingProfile) ValidatePolicy(*filter.Policy) error { return nil }

func (refusingProfile) CompileEvaluator(*filter.Policy, filter.Node) (evaluator.Program, error) {
	return nil, filter.ErrUnsupportedOperation
}

func TestBuiltinOverrideAndClearingProfile(t *testing.T) {
	t.Parallel()

	e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(refusingProfile{}), evaluator.WithProfile(nil))
	require.NoError(t, err)
	// The profile refuses every filter; the default evaluator, restored by WithProfile(nil), accepts this one.
	require.NoError(t, e.Validate(filtertest.MustParseFilter(t, `name.contains(name.substring(0))`)))

	handler := func([]filter.Node) (filter.Node, error) { return &filter.LiteralNode{Value: "overridden"}, nil }
	p, err := parser.New(parser.WithAllowBuiltinOverride(true), parser.WithCustomFunctions(map[string]filter.CustomFunction{"timestamp": handler}))
	require.NoError(t, err)
	n, err := p.Parse(t.Context(), `timestamp("anything")`)
	require.NoError(t, err)
	require.Equal(t, "overridden", n.(*filter.LiteralNode).Value)

	_, err = parser.New(parser.WithTimestampParser(nil))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	_, err = filter.NewPolicy(filter.WithAnyField(true), filter.WithDecimalParser(nil))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

func TestEnvironmentOptions(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithEnvironmentOptions(cel.ParserExpressionSizeLimit(1)))
	require.NoError(t, err)
	_, err = p.Parse(t.Context(), "name == 1")
	require.ErrorIs(t, err, filter.ErrParseFailed)
}

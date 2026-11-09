// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

func TestExpectationProblems(t *testing.T) {
	t.Parallel()

	cases := []conformanceCase{{name: "a"}, {name: "b"}, {name: "shared", err: filter.ErrFieldNotAllowed}}

	assert.Empty(t, expectationProblems(map[string]Outcome{"a": {}, "b": {}}, cases))
	assert.Empty(t, expectationProblems(map[string]Outcome{"a": {}, "b": {}, "shared": {Known: "gap"}}, cases))
	assert.Equal(t, []string{
		`Expect["shared"]: a shared case may be overridden only as a Known gap`,
		`Expect["typo"]: not a conformance case`,
		`Expect["b"]: missing outcome`,
	}, expectationProblems(map[string]Outcome{"a": {}, "shared": {}, "typo": {}}, cases))
}

func TestConformanceCaseNames(t *testing.T) {
	t.Parallel()

	specific, shared := ConformanceCases(), SharedConformanceCases()
	all := append(slices.Clone(specific), shared...)
	assert.Len(t, all, len(conformanceCases()))

	slices.Sort(all)
	assert.Len(t, slices.Compact(all), len(conformanceCases()), "case names must be unique")
	assert.NotEmpty(t, shared)
}

func TestConformanceRecordsAreFresh(t *testing.T) {
	t.Parallel()

	records := ConformanceRecords()
	require.Len(t, records, 5)
	records[0][fieldName] = "changed"
	assert.Equal(t, "alice", ConformanceRecords()[0][fieldName])
}

func TestFormatSQL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1 = 1", FormatSQL("1 = 1", nil))
	assert.Equal(t,
		`q | [null, "a", b"\x00b", timestamp("2024-01-01T00:00:00.5Z"), 2.0, 1.5, 3, true, decimal.Decimal(1.25), []int{1}]`,
		FormatSQL("q", []any{
			nil, "a", []byte("\x00b"), time.Date(2024, 1, 1, 0, 0, 0, 5e8, time.UTC), 2.0, 1.5, int64(3), true,
			decimal.RequireFromString("1.25"), []int{1},
		}))
}

func TestFormatMatches(t *testing.T) {
	t.Parallel()

	assert.Equal(t, noMatch, FormatMatches(func(filter.Node, map[string]any) (bool, error) { return false, nil }, nil))
	assert.Equal(t, "1 !2 3 4 5", FormatMatches(func(_ filter.Node, r map[string]any) (bool, error) {
		if r[fieldID] == "2" {
			return false, errors.New("boom")
		}

		return true, nil
	}, nil))
	assert.Equal(t, "1 3", selected("1 !2 3"))
	assert.Equal(t, noMatch, selected("!1 !2"))
}

func TestSuggestion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `observed: "c": {Want: "x"},`, suggestion("c", "x", nil))
	assert.Equal(t, `observed: "c": {Err: filter.ErrUnsupportedType},`, suggestion("c", "", filter.ErrUnsupportedType))
	assert.Contains(t, suggestion("c", "", errors.New("boom")), "without a filter sentinel")
}

// TestRunConformanceSelfConsistent runs the suite against the default evaluator with the outcomes it produces itself, Match and
// Reference both the evaluator: it checks the runner, not the evaluator (its expectations live in filter/evaluator).
func TestRunConformanceSelfConsistent(t *testing.T) {
	t.Parallel()

	newMatch := func(policy *filter.Policy, opts ...filter.TranslatorOption) (MatchFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return e.Evaluate, nil
	}
	newTranslate := func(policy *filter.Policy, opts ...filter.TranslatorOption) (TranslateFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return func(node filter.Node) (string, error) {
			p, err := e.Prepare(node)
			if err != nil {
				return "", err
			}

			return FormatMatches(func(_ filter.Node, r map[string]any) (bool, error) { return p.Evaluate(r) }, node), nil
		}, nil
	}

	expect := map[string]Outcome{}

	for _, c := range conformanceCases() {
		policy, opts, node := c.build(t)
		run, err := newTranslate(policy, opts...)
		require.NoError(t, err)

		got, err := run(node)

		switch {
		case c.err != nil && errors.Is(err, c.err):
		case c.err != nil:
			expect[c.name] = Outcome{Want: got, Err: err, Known: "the default evaluator validates lazily"}
		default:
			expect[c.name] = Outcome{Want: got, Err: sentinelOf(err)}
		}
	}

	RunConformance(t, Backend{New: newTranslate, Match: newMatch, Reference: newMatch, Expect: expect})
}

// sentinelOf is the filter sentinel err wraps, which a later run's error matches, or err itself.
func sentinelOf(err error) error {
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.err
		}
	}

	return err
}

func TestDescribeAndObserved(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "c `a == 1`", conformanceCase{name: "c", expr: "a == 1"}.describe())
	assert.Equal(t, "c (nil filter)", conformanceCase{name: "c"}.describe())
	assert.Equal(t, "c (built)", conformanceCase{name: "c", node: func() filter.Node { return nil }, nodeDesc: "built"}.describe())
	assert.Equal(t, `"x"`, observed("x", nil))
	assert.Equal(t, "error boom", observed("", errors.New("boom")))
}

func TestHelpers(t *testing.T) {
	t.Parallel()

	node := MustParseFilter(t, `a == 1`)
	_, err := evaluator.New(MustPolicy(t, filter.WithAllowedFields("a")))
	require.NoError(t, err)
	assert.Equal(t, filter.NodeKindBinaryOp, node.Kind())
}

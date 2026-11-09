// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// TranslateFunc translates a filter with one configured translator and renders the backend output as a string, so that the
// conformance suite can compare it with an expectation. Rendering is the backend's choice: a query with its bound arguments
// ([FormatSQL]), a JSON document, or, for a backend that evaluates filters itself, the records of [ConformanceRecords] it
// selects ([FormatMatches]). A nil filter is passed as a nil node.
type TranslateFunc func(filter.Node) (string, error)

// MatchFunc reports whether one record of [ConformanceRecords] matches a filter. A backend that can run its output in-process
// (a Lua interpreter, an in-memory evaluator) implements it on its own rendering; a per-record error counts as no match.
type MatchFunc func(node filter.Node, record map[string]any) (bool, error)

// Outcome is the expected result of one conformance case on one backend: the rendered output Want, or an error matching Err with
// [errors.Is] (returned by the constructor or by the translation).
//
// Known marks a known gap: the outcome pinned by Err or Want is what the backend does today, and Known says why it is wrong (a
// silent wrong result, a sentinel that differs from the other backends). The suite still checks the pinned outcome, tolerates a
// disagreement with [Backend.Reference] and logs the gap, so it stays visible until the backend is fixed and the entry is updated.
type Outcome struct {
	Err   error
	Want  string
	Known string
}

// Backend is one filter translator (or evaluator) under the conformance suite.
type Backend struct {
	// New builds the translator under test bound to policy with the translator options opts ([filter.TranslatorOption], passed
	// through the translator's WithFilterOptions) and returns its rendered Translate. A nil policy must fail with
	// [filter.ErrPolicyRequired]. A constructor error is the outcome of the case.
	New func(policy *filter.Policy, opts ...filter.TranslatorOption) (TranslateFunc, error)
	// Match, when set, runs the backend's output in-process against each record of [ConformanceRecords] (converted to the
	// backend's storage form by the backend). The suite compares the records it selects with Reference for every case whose
	// expected outcome is a translation.
	Match func(policy *filter.Policy, opts ...filter.TranslatorOption) (MatchFunc, error)
	// Reference is the semantics Match is compared with, normally the default filter/evaluator built on the same policy and
	// options. It is required when Match is set.
	Reference func(policy *filter.Policy, opts ...filter.TranslatorOption) (MatchFunc, error)
	// Expect holds the outcome of every backend-specific case, by case name ([ConformanceCases]). A missing or unknown name fails
	// the suite, so a case added to the suite makes every backend declare its behavior. A case whose outcome every backend shares
	// ([SharedConformanceCases]) may appear only with Known set, to pin a deviation from the shared outcome.
	Expect map[string]Outcome
}

// RunConformance runs the filter conformance suite against b: one table of filters over the fields of [ConformanceRecords]
// covering every operator, function and operand shape of the filter language (comparisons, has(), string functions with literal
// and field arguments, matches(), size(), substring(), timestamps, bytes, enums, exact decimals, nested and array fields, null
// comparisons, in lists, negation, precedence, field operands) and the policy rules every translator inherits from
// [filter.Policy] (allow-list, denied fields, enum values, literal kinds, regex rules, depth and operation limits).
//
// Every case checks the outcome b declares in Expect (or the shared outcome), that Translate is deterministic and leaves the
// caller's node unchanged, and, when b sets Match, that the backend selects the same records as Reference. The suite also checks
// that a nil policy fails with [filter.ErrPolicyRequired].
func RunConformance(t *testing.T, b Backend) {
	t.Helper()

	cases := conformanceCases()
	checkExpectations(t, b, cases)

	if b.Match != nil && b.Reference == nil {
		t.Fatal("Backend.Match requires Backend.Reference")
	}

	if _, err := b.New(nil); !errors.Is(err, filter.ErrPolicyRequired) {
		t.Errorf("New(nil policy) error = %v, want %v", err, filter.ErrPolicyRequired)
	}

	for _, c := range cases {
		want, specific := b.Expect[c.name]
		if !specific {
			want = Outcome{Err: c.err}
		}

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			runCase(t, b, c, want)
		})
	}
}

// runCase checks one case against its expected outcome.
func runCase(t *testing.T, b Backend, c conformanceCase, want Outcome) {
	t.Helper()

	if want.Known != "" {
		t.Logf("known gap: %s", want.Known)
	}

	policy, opts, node := c.build(t)

	got, err := translate(t, b, c, policy, opts, node)
	if want.Err != nil {
		if !errors.Is(err, want.Err) {
			t.Fatalf("%s: got %s; want error %v\n%s", c.describe(), observed(got, err), want.Err, suggestion(c.name, got, err))
		}

		return
	}

	if err != nil || got != want.Want {
		t.Fatalf("%s: got %s; want %q\n%s", c.describe(), observed(got, err), want.Want, suggestion(c.name, got, err))
	}

	if b.Match != nil {
		compareMatches(t, b, c, want, policy, opts, node)
	}
}

// translate builds the backend for c and translates node twice, checking that the output is stable and the node unchanged.
func translate(t *testing.T, b Backend, c conformanceCase, policy *filter.Policy, opts []filter.TranslatorOption, node filter.Node) (string, error) {
	t.Helper()

	run, err := b.New(policy, opts...)
	if err != nil {
		return "", err
	}

	before := cloneNode(t, node)

	got, err := run(node)
	if err == nil && got == "" && node != nil {
		t.Errorf("%s: Translate() returned an empty output without an error", c.describe())
	}

	again, againErr := run(node)
	if again != got || (againErr == nil) != (err == nil) {
		t.Errorf("%s: Translate() is not deterministic: %s, then %s", c.describe(), observed(got, err), observed(again, againErr))
	}

	if !reflect.DeepEqual(before, node) {
		t.Errorf("%s: Translate() modified the node", c.describe())
	}

	return got, err
}

// compareMatches runs b.Match and b.Reference over [ConformanceRecords] and fails t when they select different records, unless the
// outcome is a known gap.
func compareMatches(t *testing.T, b Backend, c conformanceCase, want Outcome, policy *filter.Policy, opts []filter.TranslatorOption,
	node filter.Node,
) {
	t.Helper()

	match, err := b.Match(policy, opts...)
	if err != nil {
		t.Fatalf("%s: Match() error = %v", c.describe(), err)
	}

	ref, err := b.Reference(policy, opts...)
	if err != nil {
		t.Fatalf("%s: Reference() error = %v", c.describe(), err)
	}

	got, reference := FormatMatches(match, node), FormatMatches(ref, node)
	if selected(got) == selected(reference) {
		return
	}

	if want.Known != "" {
		t.Logf("%s: selects %s, reference %s (known gap)", c.describe(), got, reference)

		return
	}

	t.Errorf("%s: selects %s, reference selects %s", c.describe(), got, reference)
}

// selected drops the per-record errors of a [FormatMatches] rendering, keeping the selected records: an error is no match.
func selected(s string) string {
	var ids []string

	for f := range strings.FieldsSeq(s) {
		if !strings.HasPrefix(f, "!") {
			ids = append(ids, f)
		}
	}

	if len(ids) == 0 {
		return noMatch
	}

	return strings.Join(ids, " ")
}

// noMatch renders a filter that selects no record.
const noMatch = "none"

// FormatMatches renders the records of [ConformanceRecords] that match selects for node, by id in record order ("1 3"), "none"
// when it selects no record. A record whose evaluation fails is rendered as "!id" ("1 !4"); it counts as no match when two
// renderings are compared. A backend that evaluates filters itself renders its output with it.
func FormatMatches(match MatchFunc, node filter.Node) string {
	var parts []string

	for _, rec := range ConformanceRecords() {
		id, _ := rec[fieldID].(string)

		ok, err := match(node, rec)

		switch {
		case err != nil:
			parts = append(parts, "!"+id)
		case ok:
			parts = append(parts, id)
		}
	}

	if len(parts) == 0 {
		return noMatch
	}

	return strings.Join(parts, " ")
}

// observed renders a translation result for a failure message.
func observed(got string, err error) string {
	if err != nil {
		return fmt.Sprintf("error %v", err)
	}

	return fmt.Sprintf("%q", got)
}

// sentinels are the errors [suggestion] names when it renders an observed error as an expectation.
var sentinels = []struct {
	name string
	err  error
}{
	{"filter.ErrUnsupportedOperation", filter.ErrUnsupportedOperation},
	{"filter.ErrUnsupportedType", filter.ErrUnsupportedType},
	{"filter.ErrFieldTypeMismatch", filter.ErrFieldTypeMismatch},
	{"filter.ErrEnumValueNotAllowed", filter.ErrEnumValueNotAllowed},
	{"filter.ErrFieldNotAllowed", filter.ErrFieldNotAllowed},
	{"filter.ErrFunctionNotAllowed", filter.ErrFunctionNotAllowed},
	{"filter.ErrInvalidRegex", filter.ErrInvalidRegex},
	{"filter.ErrMaxDepthExceeded", filter.ErrMaxDepthExceeded},
	{"filter.ErrMaxOperationsExceeded", filter.ErrMaxOperationsExceeded},
	{"filter.ErrEmptyExpression", filter.ErrEmptyExpression},
	{"filter.ErrInvalidOptions", filter.ErrInvalidOptions},
	{"filter.ErrInvalidExpression", filter.ErrInvalidExpression},
}

// suggestion renders the observed result of a case as an Expect entry, to paste after checking that it is right.
func suggestion(name, got string, err error) string {
	if err == nil {
		return fmt.Sprintf("observed: %q: {Want: %q},", name, got)
	}

	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return fmt.Sprintf("observed: %q: {Err: %s},", name, s.name)
		}
	}

	return fmt.Sprintf("observed: %q: error without a filter sentinel: %v", name, err)
}

// cloneNode deep-copies node, so that the suite can tell whether a translation modified it.
func cloneNode(t *testing.T, node filter.Node) filter.Node {
	t.Helper()

	if node == nil {
		return nil
	}

	c, err := filter.Clone(node)
	if err != nil {
		t.Fatalf("filter.Clone() error = %v", err)
	}

	return c
}

// ConformanceCases returns the names of the backend-specific cases, the keys [Backend.Expect] must hold.
func ConformanceCases() []string {
	var names []string

	for _, c := range conformanceCases() {
		if c.err == nil {
			names = append(names, c.name)
		}
	}

	return names
}

// SharedConformanceCases returns the names of the cases whose outcome every backend shares (policy rules); [Backend.Expect] holds
// one of them only to pin a known deviation.
func SharedConformanceCases() []string {
	var names []string

	for _, c := range conformanceCases() {
		if c.err != nil {
			names = append(names, c.name)
		}
	}

	return names
}

// checkExpectations fails t when b.Expect does not hold exactly the backend-specific cases (plus shared cases marked Known).
func checkExpectations(t *testing.T, b Backend, cases []conformanceCase) {
	t.Helper()

	for _, problem := range expectationProblems(b.Expect, cases) {
		t.Error(problem)
	}
}

// expectationProblems lists what is wrong with expect: an unknown case name, a shared case overridden without Known, a
// backend-specific case without an outcome.
func expectationProblems(expect map[string]Outcome, cases []conformanceCase) []string {
	shared := make(map[string]bool, len(cases))
	for _, c := range cases {
		shared[c.name] = c.err != nil
	}

	var problems []string

	for _, name := range slices.Sorted(maps.Keys(expect)) {
		isShared, ok := shared[name]

		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("Expect[%q]: not a conformance case", name))
		case isShared && expect[name].Known == "":
			problems = append(problems, fmt.Sprintf("Expect[%q]: a shared case may be overridden only as a Known gap", name))
		}
	}

	for _, c := range cases {
		if _, ok := expect[c.name]; c.err == nil && !ok {
			problems = append(problems, fmt.Sprintf("Expect[%q]: missing outcome", c.name))
		}
	}

	return problems
}

// build returns the policy, translator options and parsed node of c.
func (c conformanceCase) build(t *testing.T) (*filter.Policy, []filter.TranslatorOption, filter.Node) {
	t.Helper()

	policy, err := filter.NewPolicy(c.policyOptions()...)
	if err != nil {
		t.Fatalf("%s: filter.NewPolicy() error = %v", c.describe(), err)
	}

	if c.node != nil {
		return policy, c.opts, c.node()
	}

	if c.expr == "" {
		return policy, c.opts, nil
	}

	p, err := filterparser.New(append([]filterparser.Option{filterparser.WithNoCache(true)}, c.parser...)...)
	if err != nil {
		t.Fatalf("%s: filterparser.New() error = %v", c.describe(), err)
	}

	node, err := p.Parse(t.Context(), c.expr)
	if err != nil {
		t.Fatalf("%s: Parse() error = %v", c.describe(), err)
	}

	return policy, c.opts, node
}

// describe names the case and its filter in a failure message.
func (c conformanceCase) describe() string {
	switch {
	case c.node != nil:
		return fmt.Sprintf("%s (%s)", c.name, c.nodeDesc)
	case c.expr == "":
		return c.name + " (nil filter)"
	default:
		return fmt.Sprintf("%s `%s`", c.name, c.expr)
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"time"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
)

// FieldReader resolves stored values. Reusable implementations must be concurrency
// safe; a request-bound reader follows its budget's concurrency guarantees.
type FieldReader interface {
	Lookup(map[string]any, string) (any, bool, error)
}

// Profile supplies the portable proto3 listing contract to filter/evaluator.
// Required fields, zero-valued optional fields, arrays and nullable presence
// fields retain the same semantics regardless of the backing store.
type Profile struct {
	containsFields []string
	maxCompileWork int
	schema         *listing.Schema
	reader         FieldReader
	budget         evaluator.Budget
}

// BindBudget binds evaluation and field reads to a request-owned budget.
// The returned profile must only be used for that request.
func (p *Profile) BindBudget(budget evaluator.Budget) evaluator.Profile {
	cloned := *p

	cloned.budget = budget
	if reader, ok := cloned.reader.(BudgetedReader); ok {
		cloned.reader = reader.BindBudget(budget)
	}

	return &cloned
}

// New constructs an explicit listing evaluation profile from metadata and a reader.
func New(schema *listing.Schema, reader FieldReader) (*Profile, error) {
	if schema == nil || nilcheck.IsNil(reader) {
		return nil, listing.ErrInvalidSchema
	}

	maxWork := evaluator.DefaultMaxDataBudget
	if r, ok := reader.(*Reader); ok {
		maxWork = r.maxWork
	}

	return &Profile{schema: schema, reader: reader, containsFields: schema.CaseInsensitiveContainsFields(), maxCompileWork: maxWork}, nil
}

// ValidatePolicy satisfies evaluator.Profile. The schema owns contract validation.
func (p *Profile) ValidatePolicy(policy *filter.Policy) error { return nil }

// CompileEvaluator snapshots a validated AST and compiles it once: field
// declarations, literal and decimal operands, zero values and patterns are
// resolved here, not per record. A request-bound program shares its profile's
// budget; an unbound program is safe for concurrent use.
func (p *Profile) CompileEvaluator(policy *filter.Policy, node filter.Node) (evaluator.Program, error) {
	if err := filter.Validate(node, policy); err != nil {
		return nil, err
	}

	snapshot, err := filter.Clone(node)
	if err != nil {
		return nil, err
	}

	matchers := make(map[string]filter.Regex)
	if err := prepareMatchers(policy, matchers, snapshot); err != nil {
		return nil, err
	}

	compiled := &program{policy: policy, reader: p.reader, budget: p.budget, precision: p.schema.TimestampPrecision()}
	if snapshot != nil {
		c := compiler{
			schema: p.schema, containsFields: p.containsFields, budget: readBudget{left: p.maxCompileWork, parent: p.budget},
			policy: policy, matchers: matchers, refs: make(map[string]*fieldRef),
		}

		compiled.root = c.compile(snapshot)
		if c.budget.exhausted {
			return nil, filter.ErrDataLimitExceeded
		}
	}

	return compiled, nil
}

type program struct {
	budget    evaluator.Budget
	reader    FieldReader
	policy    *filter.Policy
	root      *expr
	precision time.Duration
}

func (p *program) Evaluate(data map[string]any, maxWork int, decoder evaluator.ValueDecoder) (bool, error) {
	if decoder != nil {
		return false, filter.ErrInvalidOptions
	}

	if p.root == nil {
		return true, nil
	}

	e := contractEvaluator{
		policy: p.policy, reader: p.reader, precision: p.precision, bounded: true,
		budget: readBudget{left: maxWork, parent: p.budget},
	}

	value, err := e.matches(p.root, data)
	if e.budget.exhausted {
		return false, filter.ErrDataLimitExceeded
	}

	return value == trueValue, err
}

// BudgetedReader binds stored-field traversal to the same request budget as
// predicate evaluation. Custom readers with nested work should implement it.
type BudgetedReader interface {
	BindBudget(evaluator.Budget) FieldReader
}

// prepareMatchers compiles the literal patterns of the matches() calls of node, in tree order.
func prepareMatchers(policy *filter.Policy, matchers map[string]filter.Regex, node filter.Node) error {
	if node == nil {
		return nil
	}

	if err := prepareMatcher(policy, matchers, node); err != nil {
		return err
	}

	for child := range node.Children() {
		if err := prepareMatchers(policy, matchers, child); err != nil {
			return err
		}
	}

	return nil
}

func prepareMatcher(policy *filter.Policy, matchers map[string]filter.Regex, node filter.Node) error {
	call, ok := node.(*filter.CallNode)
	if !ok || call.Op != filter.OpMatches || len(call.Args) != 1 {
		return nil
	}

	literal, ok := call.Args[0].(*filter.LiteralNode)
	if !ok {
		return nil
	}

	pattern, ok := literal.Value.(string)
	if !ok {
		return nil
	}

	if _, exists := matchers[pattern]; exists {
		return nil
	}

	matcher, err := policy.CompileRegex(pattern)
	if err != nil {
		return err
	}

	matchers[pattern] = matcher

	return nil
}

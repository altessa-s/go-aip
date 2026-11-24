// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"regexp"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Program owns a validated expression. It is immutable and safe for concurrent use.
type Program struct {
	options schemaOptions
	root    *expression
	// foldedNeedles caches selected literal needles while SQL keeps the original bind values.
	foldedNeedles      map[*expression]string
	jsonbStringCompare func(string, string) int
	contract           bool
}

// expression is a typed node of a compiled filter.
type expression = sqltype.Expr[field]

// Compile validates every branch and snapshots all literal values.
func (s *Schema) Compile(policy *filter.Policy, node filter.Node) (*Program, error) {
	return s.compile(policy, node, s.ContractMode())
}

// CompileContract compiles an expression using the listing contract even when the schema was created without contract mode.
func (s *Schema) CompileContract(policy *filter.Policy, node filter.Node) (*Program, error) {
	return s.compile(policy, node, true)
}

func (s *Schema) compile(policy *filter.Policy, node filter.Node, contract bool) (*Program, error) {
	if err := s.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	var validationErr error
	if policy.PortableContract() {
		validationErr = filter.Validate(node, policy)
	} else {
		validationErr = policy.CheckFields(node)
	}

	if validationErr != nil {
		return nil, validationErr
	}

	if node == nil {
		return &Program{}, nil
	}

	d := &compiler{schema: s, policy: policy, literals: &budget{left: s.opts.maxDataBudget, options: s.opts}, contract: contract}
	c := sqltype.Checker[field]{Dialect: d, Policy: policy, TypeError: typeError, Unsupported: unsupported, Contract: contract,
		MaxArrayDimensions: s.opts.maxArrayDimensions, NormalizeDecimals: true, SubstringArgFirst: true}

	root, err := c.Check(node)
	if err != nil {
		return nil, err
	}

	p := &Program{options: s.opts, root: root, jsonbStringCompare: s.opts.jsonbStringCompare, contract: contract}
	if err := p.prepare(root, d.literals); err != nil {
		return nil, err
	}

	return p, nil
}

// CompileEvaluator compiles a filter to the evaluator-neutral program contract.
func (s *Schema) CompileEvaluator(policy *filter.Policy, node filter.Node) (evaluator.Program, error) {
	return s.Compile(policy, node)
}

// prepare finishes contextual list inference and caches folded literal needles for evaluation.
func (p *Program) prepare(e *expression, b *budget) error {
	if e.Tag == filter.NodeKindList && e.Kind == sqlbase.Unknown {
		sqltype.SetListKind(e, sqlbase.Text)
	}

	if e.Op == filter.OpContains && e.Children[0].Field.foldContains {
		if needle, ok := e.Children[1].Value.(string); ok {
			if err := b.Spend(len(needle)); err != nil {
				return err
			}

			if p.foldedNeedles == nil {
				p.foldedNeedles = make(map[*expression]string)
			}

			p.foldedNeedles[e] = evaluator.SimpleLower15_1(needle)
		}
	}

	for _, child := range e.Children {
		if err := p.prepare(child, b); err != nil {
			return err
		}
	}

	return nil
}

// compiler is the PostgreSQL [sqltype.Dialect] of one compilation: every declared field and literal is accepted, and
// literals are normalized to the values the evaluator compares.
type compiler struct {
	schema   *Schema
	policy   *filter.Policy
	literals *budget
	contract bool
}

func (c *compiler) Field(name string) (field, sqlbase.Kind, bool, error) {
	f, err := c.schema.field(name)
	if err != nil {
		return field{}, sqlbase.Unknown, false, err
	}

	return f, f.kind, f.array, nil
}

func (c *compiler) Hint(name string) (sqlbase.Kind, error) {
	f, err := c.schema.field(name)
	return f.kind, err
}

func (*compiler) InferKind(v any) sqlbase.Kind { return sqltype.InferKind(v) }

func (c *compiler) Literal(x *filter.LiteralNode, kind sqlbase.Kind) (any, error) {
	value := x.Value
	if x.Text != "" {
		if kind == sqlbase.Numeric {
			value = x.Text
		}

		if kind == sqlbase.JSON {
			return parseJSON([]byte(x.Text), c.literals)
		}
	}

	return normalize(value, kind, nil, c.literals)
}

// Comparison refuses ordering whole JSONB values without a comparator matching the database collation.
func (c *compiler) Comparison(op filter.Operator, a, b *expression) error {
	if op.IsComparison() && !op.IsEquality() && (a.Kind == sqlbase.JSON || b.Kind == sqlbase.JSON) && c.schema.opts.jsonbStringCompare == nil {
		return coreerrs.Wrap(filter.ErrUnsupportedOperation, "JSONB ordering requires a comparator matching the database collation")
	}

	return nil
}

// Regex applies the [filter.PortableRegex] contract rule through [sqltype.CheckPortableRegex], the rule the MariaDB and
// ClickHouse schema translators and [filter.Validate] apply, and, outside the listing contract, matches the pattern in
// PostgreSQL's native mode, where dot matches a newline.
func (c *compiler) Regex(e *expression, pattern string) error {
	if c.policy.ContractRuleEnabled(filter.PortableRegex) {
		if err := sqltype.CheckPortableRegex(pattern); err != nil {
			return err
		}
	}

	if !c.contract && e.RE2 {
		// The user pattern has already passed policy validation. Do not charge the synthetic mode wrapper against its
		// length limit.
		matcher, err := regexp.Compile("(?s:" + pattern + ")")
		if err != nil {
			return err
		}

		e.Matcher = matcher
	}

	return nil
}

func typeError() error {
	return coreerrs.Wrap(filter.ErrFieldTypeMismatch, "incompatible PostgreSQL operand types")
}

func unsupported() error {
	return coreerrs.Wrap(filter.ErrUnsupportedOperation, "unsupported PostgreSQL expression")
}

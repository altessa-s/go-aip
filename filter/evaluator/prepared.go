// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"time"

	"github.com/altessa-s/go-aip/filter"
)

// Prepared is a filter an [Evaluator] has validated once ([Evaluator.Prepare]): [Prepared.Evaluate] tests data against it without
// validating it again. A Prepared is immutable and safe for concurrent use.
type Prepared struct {
	config  *executionConfig
	node    filter.Node
	program Program // the program a profile compiled ([Compiler]), nil without a profile
	// regexes holds the compiled literal patterns of the matches() calls of node, so that an evaluation finds them without the
	// shared regex cache.
	regexes map[string]filter.Regex
}

// Prepare takes a deep copy of node, validates the copy ([Evaluator.Validate]) — under [WithProfile], compiles it with the profile's
// [Compiler] — and returns it ready for repeated evaluation:
// changing node or its literal values afterwards does not affect the Prepared. A node Prepare cannot copy — a custom [filter.Node] or a
// literal of a mutable type other than []byte — fails with [filter.ErrInvalidExpression] or [filter.ErrUnsupportedType]. The operation and depth
// limits are checked before the copy, so a node beyond them fails without being copied.
func (e *Evaluator) Prepare(node filter.Node) (*Prepared, error) {
	if t := e.config.telemetry; t != nil {
		defer observeSince(t.prepare, time.Now())
	}

	if err := e.config.CheckLimits(node); err != nil {
		return nil, err
	}

	snapshot, err := filter.Clone(node)
	if err != nil {
		return nil, err
	}

	if e.config.compiler != nil {
		var program Program
		if program, err = e.config.compiler.CompileEvaluator(e.config.Policy, snapshot); err != nil {
			return nil, err
		}

		return &Prepared{config: e.config, program: program}, nil
	}

	if err = e.Validate(snapshot); err != nil {
		return nil, err
	}

	return &Prepared{config: e.config, node: snapshot, regexes: LiteralPatterns(e.config.Policy, snapshot)}, nil
}

// LiteralPatterns compiles the literal patterns of the matches() calls of node through the policy's regex rules and cache
// ([filter.Policy.CompileRegex]), keyed by pattern, for a [Program] that tests them on every record without the shared cache. A
// pattern the policy rejects is left out, so that the evaluation reports its error; the map is nil without a literal pattern.
func LiteralPatterns(policy *filter.Policy, node filter.Node) map[string]filter.Regex {
	var regexes map[string]filter.Regex

	filter.Walk(node, func(n filter.Node) bool {
		call, ok := n.(*filter.CallNode)
		if !ok || call.Op != filter.OpMatches || len(call.Args) != 1 {
			return true
		}

		literal, ok := call.Args[0].(*filter.LiteralNode)
		if !ok {
			return true
		}

		pattern, ok := literal.Value.(string)
		if _, seen := regexes[pattern]; !ok || seen {
			return true
		}

		if re, err := policy.CompileRegex(pattern); err == nil {
			if regexes == nil {
				regexes = make(map[string]filter.Regex)
			}

			regexes[pattern] = re
		}

		return true
	})

	return regexes
}

// Evaluate reports whether data matches the prepared filter, as [Evaluator.Evaluate] does without validating the filter again.
func (p *Prepared) Evaluate(data map[string]any) (bool, error) {
	if t := p.config.telemetry; t != nil {
		defer observeSince(t.evaluate, time.Now())
	}

	if p.program != nil {
		return p.program.Evaluate(data, p.config.maxDataBudget, p.config.valueDecoder)
	}

	return evaluate(p.config, p.node, data, p.regexes)
}

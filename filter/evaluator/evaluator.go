// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/internal/typename"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// RegexCacheStats holds a point-in-time snapshot of regex pattern cache
// performance counters. All values are cumulative since the last
// [ResetRegexCacheStats] call (or since process start).
type RegexCacheStats struct {
	// Hits is the number of lookups served from the cache.
	Hits uint64

	// Misses is the number of lookups that required pattern compilation.
	Misses uint64

	// Size is the current number of cached compiled patterns.
	Size int64
}

// HitRate returns the cache hit rate as a float64 in [0.0, 1.0].
// Returns 0 when there have been no lookups.
func (s RegexCacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}

	return float64(s.Hits) / float64(total)
}

// TotalLookups returns the total number of regex cache lookups tracked.
func (s RegexCacheStats) TotalLookups() uint64 {
	return s.Hits + s.Misses
}

// RegexCacheStatsSnapshot returns a point-in-time snapshot of the regex
// pattern cache performance counters.
func (e *Evaluator) RegexCacheStatsSnapshot() RegexCacheStats {
	hits, misses, size := e.config.RegexCacheStats()

	return RegexCacheStats{
		Hits:   hits,
		Misses: misses,
		Size:   int64(size),
	}
}

// ResetRegexCacheStats zeroes all performance counters (hits, misses) without
// affecting the cached patterns or their compiled state.
func (e *Evaluator) ResetRegexCacheStats() {
	e.config.ResetRegexCacheStats()
}

// Evaluator evaluates a filter AST against an in-memory map.
//
// An Evaluator holds only its immutable configuration: every [Evaluator.Evaluate]
// call walks the tree with its own per-call state (data, depth and operation
// counters), so one Evaluator is safe for concurrent use by multiple goroutines.
type Evaluator struct {
	config *executionConfig
}

// evaluation is the per-call state of one [Evaluator.Evaluate] call. It is the
// [Visitor] that walks the tree, so concurrent calls never share counters or data.
type evaluation struct {
	Reader  // the data and the work left for reading it ([WithMaxDataBudget])
	Guard   // the operation and depth limits
	config  *executionConfig
	regexes map[string]filter.Regex // compiled matches() patterns of a prepared filter ([Prepared])
}

// newEvaluation returns pooled per-call state for reading data with config; [evaluation.release] returns it to the pool.
func newEvaluation(config *executionConfig, data map[string]any) *evaluation {
	ev := evaluationPool.Get().(*evaluation) //nolint:errcheck // Type assertion from pool is safe by design
	// A pooled evaluation is zero but for the memo's capacity ([evaluation.release]).
	ev.config = config
	ev.reset(data, config.MaxDataBudget(), config.valueEncoding, true)
	ev.Guard.Reset(config.MaxOperations(), config.MaxDepth())

	return ev
}

// release drops the references to the data, keeping the memo's capacity, and returns e to the pool.
func (e *evaluation) release() {
	e.Release()
	e.config, e.regexes, e.Guard = nil, nil, Guard{}
	evaluationPool.Put(e)
}

// New creates a new in-memory evaluator over policy with the given options.
// The policy holds the field rules in CEL-side names and may be shared with
// translators; [WithFilterOptions] maps them to data keys. It returns
// [filter.ErrPolicyRequired] for a nil policy, and profile validation errors
// before the first evaluation.
func New(policy *filter.Policy, opts ...Option) (*Evaluator, error) {
	ctx, err := newExecutionConfig(policy, opts...)
	if err != nil {
		return nil, err
	}

	return &Evaluator{config: ctx}, nil
}

// Evaluate returns true if data matches the filter node. It is safe to call
// concurrently on a shared Evaluator. It validates the full expression first
// ([Evaluator.Validate]), including fields in skipped branches; a nil node
// matches every record. Under [WithProfile] the profile's compiled [Program]
// decides the match; [Evaluator.Prepare] validates a filter once for repeated
// evaluation.
//
// Without a profile a null or absent operand follows SQL's three-valued logic: a predicate it makes unknown (an ordering comparison, a
// bare boolean, size() or substring() of a null value, a substring() out of
// range, a null string-function argument) is absorbed by && and || as SQL's AND
// and OR absorb unknown, stays unknown under !, and a filter that is unknown as
// a whole does not match: Evaluate returns false and a nil error. Any other
// evaluation error (a value of an incomparable type, the data budget, the
// operation limits) is returned.
func (e *Evaluator) Evaluate(node filter.Node, data map[string]any) (bool, error) {
	if t := e.config.telemetry; t != nil {
		defer observeSince(t.evaluate, time.Now())
	}

	if e.config.compiler != nil {
		program, err := e.config.compiler.CompileEvaluator(e.config.Policy, node)
		if err != nil {
			return false, err
		}

		return program.Evaluate(data, e.config.maxDataBudget, e.config.valueDecoder)
	}

	if err := e.Validate(node); err != nil {
		return false, err
	}

	return evaluate(e.config, node, data, nil)
}

// evaluate tests data against node, validated, with config; regexes holds compiled matches() patterns.
func evaluate(config *executionConfig, node filter.Node, data map[string]any, regexes map[string]filter.Regex) (bool, error) {
	if node == nil {
		return true, nil
	}

	ev := newEvaluation(config, data)
	ev.regexes = regexes

	result, err := node.Accept(ev)
	if dataErr := ev.Err(); dataErr != nil {
		// What the evaluation found before the budget ran out is no answer, nor is an error it caused (a value left unconverted).
		result, err = nil, dataErr
	}

	if err == nil && result == nil {
		err = errUnknownResult
	}

	if isUnknown(err) {
		// A filter unknown as a whole, as SQL's WHERE finds it, does not match.
		result, err = false, nil
	}

	ev.release()

	if err != nil {
		return false, err
	}

	b, ok := result.(bool)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "expression must evaluate to bool, got %s", typename.Of(result))
	}

	return b, nil
}

// VisitLiteral returns the literal value.
func (e *evaluation) VisitLiteral(n *filter.LiteralNode) (any, error) {
	if err := e.Op(); err != nil {
		return nil, err
	}

	return n.Value, nil
}

// VisitIdent looks up the field in the data map, supporting dot notation.
func (e *evaluation) VisitIdent(n *filter.IdentNode) (any, error) {
	return e.identValue(n.Name, e.config.Field(n.Name))
}

// identValue is [evaluation.VisitIdent] of the field name, which f describes ([filter.Policy.Field]).
func (e *evaluation) identValue(name string, f filter.FieldPolicy) (any, error) {
	if err := e.Op(); err != nil {
		return nil, err
	}

	if !f.Allowed() {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", name)
	}

	var v any

	switch {
	case f.IsArray():
		v, _, _ = e.ResolvePath(e.data, f.Path())
	default:
		v = e.ReadPath(f.Path())
	}

	return v, e.Err()
}

// VisitBinaryOp evaluates binary operations.
func (e *evaluation) VisitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	switch n.Op {
	case filter.OpAnd:
		return e.evalLogicalAnd(n.Left, n.Right)
	case filter.OpOr:
		return e.evalLogicalOr(n.Left, n.Right)
	case filter.OpIn:
		return e.evalIn(n.Left, n.Right)
	default:
		return e.evalComparison(n.Op, n.Left, n.Right)
	}
}

// VisitUnaryOp evaluates unary operations.
func (e *evaluation) VisitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	if n.Op != filter.OpNot {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
	}

	val, err := n.Operand.Accept(e)
	if err != nil {
		return nil, err // ! keeps an unknown operand unknown
	}

	if val == nil {
		return nil, errUnknownNotOperand
	}

	b, ok := val.(bool)
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "! requires bool operand, got %s", typename.Of(val))
	}

	return !b, nil
}

// VisitCall evaluates function calls.
func (e *evaluation) VisitCall(n *filter.CallNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	// Every supported call operates on a target. A hand-built node, a node
	// returned by a custom function, or a target-less call such as
	// `contains("x")` must fail as malformed rather than dereference nil.
	if n.Target == nil {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "%v call without a target", n.Op)
	}

	switch n.Op {
	case filter.OpContains:
		return e.evalStringFunc(n, strings.Contains)
	case filter.OpStartsWith:
		return e.evalStringFunc(n, strings.HasPrefix)
	case filter.OpEndsWith:
		return e.evalStringFunc(n, strings.HasSuffix)
	case filter.OpMatches:
		return e.evalMatches(n)
	case filter.OpHas, filter.OpExists:
		return e.evalHas(n)
	case filter.OpSize:
		return e.evalSize(n)
	case filter.OpSubstring:
		return e.evalSubstring(n)
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v", n.Op)
	}
}

// VisitList evaluates a list literal.
func (e *evaluation) VisitList(n *filter.ListNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	result := make([]any, 0, len(n.Elements))
	for _, elem := range n.Elements {
		val, err := elem.Accept(e)
		if err != nil {
			return nil, err
		}

		result = append(result, val)
	}

	return result, nil
}

// evalLogicalAnd is && ([evaluation.evalLogical]): false decides it.
func (e *evaluation) evalLogicalAnd(left, right filter.Node) (any, error) {
	return e.evalLogical(left, right, logicalAnd)
}

// evalLogicalOr is || ([evaluation.evalLogical]): true decides it.
func (e *evaluation) evalLogicalOr(left, right filter.Node) (any, error) {
	return e.evalLogical(left, right, logicalOr)
}

// evalLogical evaluates a binary boolean operator with SQL's three-valued logic, whatever the order of its operands: an operand
// equal to shortCircuit (false for &&, true for ||) decides the result, and a data-driven unknown operand ([unknownError]) is
// absorbed by it — unknown || true holds, unknown && false is false — and is the result otherwise. The right operand is skipped
// only when the left one decides the result. Any other error is returned as it is.
func (e *evaluation) evalLogical(left, right filter.Node, op logicalOperator) (any, error) {
	shortCircuit := op.shortCircuit

	lb, lerr := e.evalBoolOperand(left, op)
	if lerr != nil && !isUnknown(lerr) {
		return nil, lerr
	}

	if lerr == nil && lb == shortCircuit {
		return shortCircuit, nil
	}

	rb, rerr := e.evalBoolOperand(right, op)
	if rerr != nil && !isUnknown(rerr) {
		return nil, rerr
	}

	switch {
	case rerr == nil && rb == shortCircuit:
		return shortCircuit, nil
	case lerr != nil:
		return nil, lerr
	case rerr != nil:
		return nil, rerr
	default:
		return rb, nil
	}
}

// evalBoolOperand evaluates n, an operand of op, and asserts the result is a bool; a null result is a data-driven unknown
// ([unknownError]).
func (e *evaluation) evalBoolOperand(n filter.Node, op logicalOperator) (bool, error) {
	v, err := n.Accept(e)
	if err != nil {
		return false, err
	}

	if v == nil {
		return false, op.null
	}

	b, ok := v.(bool)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires bool operands", op.name)
	}

	return b, nil
}

// evalSize returns the size of a string, in Unicode code points as CEL
// defines it, or of a slice.
func (e *evaluation) evalSize(n *filter.CallNode) (any, error) {
	target, err := n.Target.Accept(e)
	if err != nil {
		return nil, err
	}

	switch v := target.(type) {
	case string:
		if !e.budget.chargeScan(len(v)) {
			return nil, e.Err()
		}

		return int64(utf8.RuneCountInString(v)), nil
	case []any:
		return int64(len(v)), nil
	case nil:
		if e.isArrayField(n.Target) {
			return int64(0), nil // an absent or null array is empty
		}

		return nil, errUnknownSizeTarget
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "size() not supported for %s", typename.Of(target))
	}
}

// Ensure evaluation implements Visitor.
var _ filter.Visitor = (*evaluation)(nil)

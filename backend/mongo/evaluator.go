// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// A Schema is the evaluator profile of the MongoDB translator: evaluator.New(policy, evaluator.WithProfile(schema)) matches a
// record exactly when the query the translator builds over the same policy and schema matches the document MongoDB stores for it.
// [Schema.BindEvaluator] binds the schema to the evaluator's policy and value encoding; the [evaluator.Compiler] it returns
// validates a filter with the rules of the translator (a filter the translator rejects fails whatever the data) and compiles it
// to a program that reads every record through an [evaluator.Reader], so the data budget, the stored forms and the paths through
// lists of documents are those of the default evaluator.

// BindEvaluator binds the schema to the evaluator's policy (the omitted paths resolved through its field mapping) and to the value
// encoding the programs read data through (nil for none); it fails with [filter.ErrInvalidOptions] when the policy's field
// declarations conflict with the schema ([Schema.ValidatePolicy]). The returned compiler implements [evaluator.FieldAccessor].
func (s *Schema) BindEvaluator(policy *filter.Policy, enc evaluator.ValueEncoding) (evaluator.Compiler, error) {
	bound, err := s.BindPolicy(policy)
	if err != nil {
		return nil, err
	}

	return &evalCompiler{schema: bound, enc: enc}, nil
}

// evalCompiler is a [Schema] bound to an evaluator ([Schema.BindEvaluator]). It is immutable and safe for concurrent use.
type evalCompiler struct {
	schema *Schema
	enc    evaluator.ValueEncoding
}

// CompileEvaluator validates node with the rules of the MongoDB translator and compiles it; the program holds node, which
// [evaluator.Evaluator.Prepare] copies first. A nil node matches every document, as the translator's empty query does.
func (c *evalCompiler) CompileEvaluator(policy *filter.Policy, node filter.Node) (evaluator.Program, error) {
	if err := policy.CheckLimits(node); err != nil {
		return nil, err
	}

	v := &evalValidator{policy: policy, schema: c.schema}
	if err := v.validate(node); err != nil {
		return nil, err
	}

	p := &evalProgram{compiler: c, policy: policy, node: node, regexes: evaluator.LiteralPatterns(policy, node)}
	if presence := c.schema.PresenceFields(); len(presence) != 0 && node != nil {
		// The translator reads the filter in negation normal form; the operations the rewrite adds are allowed for.
		originalOps := nodeCount(node)
		p.node = filter.NegationNormalForm(node, policy.ArrayFields(), presence)
		p.syntheticOps = max(0, nodeCount(p.node)-originalOps)
	}

	return p, nil
}

// Lookup returns the value of the field name in data as the predicates of a program read it, for a caller that needs the same
// value outside a filter: the stored form — pointers followed, typed maps, bson documents and structs read as documents, named
// scalars as their base values — through which a dotted path steps; a path through a list of documents yields the list of the
// values found; a field declared by [WithSchemaOmitEmpty] whose value is empty is absent and one declared by
// [WithSchemaZeroWhenAbsent] that the data lacks is its zero value. present reports whether the path exists (a null value
// included). It fails with [filter.ErrFieldNotAllowed] outside the allow-list and with [filter.ErrDataLimitExceeded] beyond maxWork.
func (c *evalCompiler) Lookup(policy *filter.Policy, data map[string]any, name string, maxWork int, _ evaluator.ValueDecoder) (any, bool, error) {
	e := newEvaluation(c, policy, nil, data, maxWork)
	f, err := e.queryField(&filter.IdentNode{Name: name})
	e.release()

	if err != nil {
		return nil, false, err
	}

	return f.value, f.present, nil
}

// evalProgram is a filter validated and compiled by an [evalCompiler]. It is immutable and safe for concurrent use.
type evalProgram struct {
	compiler     *evalCompiler
	policy       *filter.Policy
	node         filter.Node // the filter, in negation normal form when the schema declares presence fields
	syntheticOps int         // the operations the negation normal form added
	// regexes holds the compiled literal patterns of the matches() calls of node, so that an evaluation finds them without the
	// shared regex cache.
	regexes map[string]filter.Regex
}

// Evaluate reports whether data matches the program, reading at most maxWork units of data. The program reads data through the
// bound value encoding, so decoder, which adapts SQL values, is not used. Every per-document outcome of a predicate is a bool; an
// error is a malformed filter or a limit.
func (p *evalProgram) Evaluate(data map[string]any, maxWork int, _ evaluator.ValueDecoder) (bool, error) {
	if p.node == nil {
		return true, nil
	}

	e := newEvaluation(p.compiler, p.policy, p.regexes, data, maxWork)
	e.Guard.Reset(p.policy.MaxOperations()+p.syntheticOps, p.policy.MaxDepth())

	result, err := e.acceptPredicate(p.node)
	if dataErr := e.Err(); dataErr != nil {
		// What the evaluation found before the budget ran out is no answer, nor is an error it caused (a value left unconverted).
		result, err = nil, dataErr
	}

	e.release()

	if err != nil {
		return false, err
	}

	b, ok := result.(bool)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "expression must evaluate to bool, got %s", resultTypeName(result))
	}

	return b, nil
}

// resultTypeName names the type of a non-bool result in an error message: null for nil.
func resultTypeName(v any) string {
	if v == nil {
		return "null"
	}

	return operandTypeName(v)
}

// nodeCount returns the number of nodes of node, which the operation limit counts.
func nodeCount(node filter.Node) int {
	if node == nil {
		return 0
	}

	switch n := node.(type) {
	case *filter.IdentNode, *filter.LiteralNode:
		return 1
	case *filter.BinaryOpNode:
		return 1 + nodeCount(n.Left) + nodeCount(n.Right)
	case *filter.UnaryOpNode:
		return 1 + nodeCount(n.Operand)
	case *filter.CallNode:
		count := 1 + nodeCount(n.Target)
		for _, arg := range n.Args {
			count += nodeCount(arg)
		}

		return count
	case *filter.ListNode:
		count := 1
		for _, element := range n.Elements {
			count += nodeCount(element)
		}

		return count
	default:
		count := 1
		for child := range node.Children() {
			count += nodeCount(child)
		}

		return count
	}
}

// evaluation is the per-call state of one evaluation of an [evalProgram]: the data reader, the operation and depth limits, and
// the bound schema and policy. It is the [filter.Visitor] that walks the filter.
type evaluation struct {
	evaluator.Reader
	evaluator.Guard

	schema  *Schema
	policy  *filter.Policy
	regexes map[string]filter.Regex
}

// evaluationPool recycles per-call state so an evaluation does not allocate it.
var evaluationPool = sync.Pool{New: func() any { return new(evaluation) }}

// newEvaluation returns pooled per-call state for reading data with the bound compiler c under policy, with the compiled
// matches() patterns regexes; [evaluation.release] returns it to the pool.
func newEvaluation(c *evalCompiler, policy *filter.Policy, regexes map[string]filter.Regex, data map[string]any, maxWork int) *evaluation {
	e := evaluationPool.Get().(*evaluation) //nolint:errcheck // Type assertion from pool is safe by design
	e.schema, e.policy, e.regexes = c.schema, policy, regexes
	e.Reader.Reset(data, maxWork, c.enc)
	e.Guard.Reset(policy.MaxOperations(), policy.MaxDepth())

	return e
}

// release drops the references to the data, keeping the reader's memo, and returns e to the pool.
func (e *evaluation) release() {
	e.Release()
	e.schema, e.policy, e.regexes, e.Guard = nil, nil, nil, evaluator.Guard{}
	evaluationPool.Put(e)
}

// VisitLiteral returns the literal value.
func (e *evaluation) VisitLiteral(n *filter.LiteralNode) (any, error) {
	if err := e.Op(); err != nil {
		return nil, err
	}

	return n.Value, nil
}

// VisitIdent reads the field in its stored form: a field declared by [filter.WithArrayFields] through lists of documents, any
// other through documents.
func (e *evaluation) VisitIdent(n *filter.IdentNode) (any, error) {
	if err := e.Op(); err != nil {
		return nil, err
	}

	f := e.policy.Field(n.Name)
	if !f.Allowed() {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", n.Name)
	}

	var v any

	if f.IsArray() {
		v, _, _ = e.ResolvePath(e.Data(), f.Path())
	} else {
		v = e.ReadPath(f.Path())
	}

	return v, e.Err()
}

// VisitBinaryOp evaluates && and || and the comparisons.
func (e *evaluation) VisitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	switch n.Op {
	case filter.OpAnd:
		return e.evalLogical(n.Left, n.Right, "&&", false)
	case filter.OpOr:
		return e.evalLogical(n.Left, n.Right, "||", true)
	case filter.OpIn:
		return e.mongoIn(n.Left, n.Right)
	default:
		return e.mongoComparison(n.Op, n.Left, n.Right)
	}
}

// evalLogical evaluates a binary boolean operator named name: an operand equal to shortCircuit (false for &&, true for ||)
// decides the result, and the right operand is skipped when the left one does. An error is returned as it is.
func (e *evaluation) evalLogical(left, right filter.Node, name string, shortCircuit bool) (any, error) {
	lb, err := e.evalBoolOperand(left, name)
	if err != nil {
		return nil, err
	}

	if lb == shortCircuit {
		return shortCircuit, nil
	}

	rb, err := e.evalBoolOperand(right, name)
	if err != nil {
		return nil, err
	}

	return rb, nil
}

// evalBoolOperand evaluates n, an operand of the logical operator name, and asserts the result is a bool.
func (e *evaluation) evalBoolOperand(n filter.Node, name string) (bool, error) {
	v, err := e.acceptPredicate(n)
	if err != nil {
		return false, err
	}

	if v == nil {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires bool operands, got null", name)
	}

	b, ok := v.(bool)
	if !ok {
		return false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires bool operands", name)
	}

	return b, nil
}

// VisitUnaryOp evaluates !, with the type guards of the translator for a presence field.
func (e *evaluation) VisitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	if n.Op != filter.OpNot {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
	}

	var target *filter.IdentNode

	switch operand := n.Operand.(type) {
	case *filter.CallNode:
		// A negated string function reading a presence field holds only on strings.
		if operand.Op.IsStringOp() {
			if holds, applies := e.presenceStringGuard(operand); applies && !holds {
				return false, e.Err()
			}
		}
	case *filter.BinaryOpNode:
		if operand.Op == filter.OpIn {
			target, _ = operand.Left.(*filter.IdentNode)
		}
	}

	if target != nil && e.schema.IsPresenceField(target.Name) {
		field, err := e.queryField(target)
		if err != nil {
			return nil, err
		}

		if field.value == nil {
			return false, nil
		}
	}

	val, err := e.acceptPredicate(n.Operand)
	if err != nil {
		return nil, err
	}

	b, ok := val.(bool)
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "! requires bool operand, got %s", resultTypeName(val))
	}

	return !b, nil
}

// VisitCall evaluates the string functions, matches() and has(); size() and substring() are operands of a comparison.
func (e *evaluation) VisitCall(n *filter.CallNode) (any, error) {
	if err := e.Enter(); err != nil {
		return nil, err
	}
	defer e.Leave()

	// Every supported call operates on a target. A hand-built node, a node returned by a custom function, or a target-less call
	// such as `contains("x")` must fail as malformed rather than dereference nil.
	if n.Target == nil {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "%v call without a target", n.Op)
	}

	switch n.Op {
	case filter.OpContains, filter.OpStartsWith, filter.OpEndsWith:
		if isStringExprCall(n) {
			return e.exprStringCall(n)
		}

		return e.mongoStringCall(n, "string function", stringTest(n.Op))
	case filter.OpMatches:
		return e.mongoStringCall(n, "matches()", e.regexTest)
	case filter.OpHas, filter.OpExists:
		return e.mongoHas(n)
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

// stringTest returns the test of contains/startsWith/endsWith (op) against an argument.
func stringTest(op filter.Operator) func(arg string) (func(string) bool, error) {
	fn := strings.HasSuffix

	switch op {
	case filter.OpContains:
		fn = strings.Contains
	case filter.OpStartsWith:
		fn = strings.HasPrefix
	default:
	}

	return func(arg string) (func(string) bool, error) {
		return func(s string) bool { return fn(s, arg) }, nil
	}
}

// regexTest returns the test of matches() against pattern: the pattern the program compiled, or the policy's regex cache's.
func (e *evaluation) regexTest(pattern string) (func(string) bool, error) {
	re, ok := e.regexes[pattern]
	if !ok {
		var err error
		if re, err = e.policy.CompileRegex(pattern); err != nil {
			return nil, err
		}
	}

	return re.MatchString, nil
}

// chargeStringTest charges the data budget for testing the data string s with the string function op and its argument arg.
func (e *evaluation) chargeStringTest(op filter.Operator, s, arg string) bool {
	if op == filter.OpMatches {
		return e.ChargeRegex(len(s), len(arg))
	}

	return e.ChargeScan(len(s))
}

// sizeOf returns the size of a list or, in code points, of a string, whose scan it charges.
func (e *evaluation) sizeOf(v any) (int64, bool) {
	switch x := v.(type) {
	case []any:
		return int64(len(x)), true
	case string:
		if !e.ChargeScan(len(x)) {
			return 0, false
		}

		return int64(utf8.RuneCountInString(x)), true
	default:
		return 0, false
	}
}

// Ensure evaluation implements Visitor.
var _ filter.Visitor = (*evaluation)(nil)

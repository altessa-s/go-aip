// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"fmt"
	"strings"

	"github.com/altessa-s/go-aip/filter"

	metrics "github.com/altessa-s/go-aip/metrics"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FilterTranslator converts filter AST nodes to Lua boolean expressions.
type FilterTranslator struct {
	predicateRenderer func(filter.Node) (string, bool, error)
	telemetry         *metrics.TranslateTimer
	literalEncoder    func(any) (any, error)
	config            *filter.Policy
	tableVar          string
	depth             filter.DepthGuard
}

// NewFilterTranslator creates a new Lua translator over policy. The rendered expression reads the record from the Lua variable
// [WithFilterTableVar] names ([DefaultTableVar] by default). Returns [filter.ErrPolicyRequired] for a nil policy. The policy holds
// the field rules in CEL-side names and may be shared with other translators; [WithFilterOptions] maps them to storage names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	ctx, err := filter.NewTranslatorContext(policy, o.policyBindings...)
	if err != nil {
		return nil, err
	}
	// Decimal literals cannot be bound exactly here yet: refuse the declaration rather than compare through a float.
	if err = ctx.RejectFieldKinds(filter.FieldKindDecimal); err != nil && o.literalEncoder == nil {
		return nil, err
	}

	return &FilterTranslator{literalEncoder: o.literalEncoder, predicateRenderer: o.predicateRenderer,
		telemetry: filter.NewTelemetry(o.collector, "lua"), config: ctx, tableVar: o.tableVar,
		depth: filter.NewDepthGuard(ctx.MaxDepth())}, nil
}

// Translate converts a filter AST node to a Lua boolean expression string.
// Returns "true" for a nil node (match all).
func (t *FilterTranslator) Translate(node filter.Node) (string, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t

	return state.translate(node)
}

func (t *FilterTranslator) translate(node filter.Node) (string, error) {
	// Authorization before shape: every field reference is checked, with the limits, before any rendering, so a denied field
	// fails with filter.ErrFieldNotAllowed even where its position (a function argument, a nested call) is unsupported.
	if err := t.config.CheckFields(node); err != nil {
		return "", err
	}

	if node == nil {
		return luaTrue, nil
	}

	t.depth.Reset()

	// A literal is a value, never a predicate. The Lua renderer quotes it, so
	// this is not an injection the way it is for the query-text backends — but
	// a bare literal still produces a filter that tests a constant, which is
	// never what the caller meant, and the sibling translators all reject it.
	if lit, ok := node.(*filter.LiteralNode); ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got literal %T", lit.Value)
	}

	result, err := t.getExprString(node)
	if err != nil {
		return "", err
	}

	return result, nil
}

// visitLiteral converts a literal value to its Lua representation.
func (t *FilterTranslator) visitLiteral(n *filter.LiteralNode) (any, error) {
	return t.formatLiteral(n.Value)
}

// visitIdent converts an identifier to a field reference string.
func (t *FilterTranslator) visitIdent(n *filter.IdentNode) (any, error) {
	field := n.Name
	if !t.config.IsFieldAllowed(field) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	return t.config.ApplyFieldMapping(field), nil
}

// visitBinaryOp converts a binary operation to a Lua expression.
func (t *FilterTranslator) visitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpAnd:
		return t.translateLogical("and", n.Left, n.Right)
	case filter.OpOr:
		return t.translateLogical("or", n.Left, n.Right)
	case filter.OpIn:
		return t.translateIn(n.Left, n.Right, false)
	default:
		return t.translateComparison(n.Op, n.Left, n.Right, false)
	}
}

// visitUnaryOp converts a unary operation to a Lua expression.
func (t *FilterTranslator) visitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	if n.Op == filter.OpNot {
		return t.translateNot(n.Operand)
	}

	return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
}

// visitCall converts a function call to a Lua expression.
func (t *FilterTranslator) visitCall(n *filter.CallNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	return t.translateCall(n, false)
}

// translateCall renders a function call, or its negation when negated ([FilterTranslator.negate]).
func (t *FilterTranslator) translateCall(n *filter.CallNode, negated bool) (any, error) {
	switch n.Op {
	case filter.OpContains:
		return t.translateContains(n.Target, n.Args, negated)
	case filter.OpStartsWith:
		return t.translateStartsWith(n.Target, n.Args, negated)
	case filter.OpEndsWith:
		return t.translateEndsWith(n.Target, n.Args, negated)
	case filter.OpMatches:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "matches is not supported by Lua translator")
	case filter.OpSize:
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "size() is not a predicate")
	case filter.OpHas, filter.OpExists:
		return t.translateHas(n.Target, negated)
	case filter.OpSubstring:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() is supported only as a comparison operand")
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v", n.Op)
	}
}

// visitList converts a list to a slice of values.
func (t *FilterTranslator) visitList(n *filter.ListNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	values := make([]any, 0, len(n.Elements))
	for _, element := range n.Elements {
		value, err := t.valueString(element)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// fieldFunc walks a record table down the path of keys it is given and returns the value there, or nil as soon as a parent is
// not a table. Indexing a number or a boolean raises a Lua error that aborts the whole script, and indexing a string reads the
// string library, so every step is guarded by type(parent) == "table": a nested field under any non-table parent is nil.
const fieldFunc = `(function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)`

// fieldRef builds a Lua expression reading a dotted field name from the record. A top-level field is a plain table access; a
// nested one goes through [fieldFunc], so a parent that is absent, null or not a table makes it nil instead of a Lua error.
// For example, "city" with tableVar "d" becomes `d["city"]` and "address.city" becomes `fieldFunc(d, "address", "city")`.
func (t *FilterTranslator) fieldRef(field string) string {
	segments := strings.Split(field, ".")
	if len(segments) == 1 {
		return t.tableVar + `["` + escapeLuaString(field) + `"]`
	}

	var b strings.Builder

	b.WriteString(fieldFunc)
	b.WriteString("(")
	b.WriteString(t.tableVar)

	for _, segment := range segments {
		b.WriteString(`, "`)
		b.WriteString(escapeLuaString(segment))
		b.WriteString(`"`)
	}

	b.WriteString(")")

	return b.String()
}

// luaOperator maps a filter operator to a Lua comparison operator string.
func (t *FilterTranslator) luaOperator(op filter.Operator) (string, error) {
	switch op {
	case filter.OpEqual:
		return "==", nil
	case filter.OpNotEqual:
		return "~=", nil
	case filter.OpLT:
		return "<", nil
	case filter.OpLTE:
		return "<=", nil
	case filter.OpGT:
		return ">", nil
	case filter.OpGTE:
		return ">=", nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}
}

// getFieldName extracts the field name from a node via Accept.
func (t *FilterTranslator) getFieldName(node filter.Node) (string, error) {
	// A call can reach here with no target at all — `contains()` parses into a
	// CallNode whose Target is nil — and accepting a nil Node panics. That
	// makes a filter expression a client controls able to crash the process, so
	// the absent target is reported as the malformed expression it is.
	if err := filter.CheckFieldReference(node); err != nil {
		return "", err
	}

	// CheckFieldReference admits size() of a field, the left operand of a comparison, which translateComparison renders
	// before it asks for a field name. Anywhere else (an in list, has(), a string function, a nested size() or substring())
	// the call is not a field: reading its target instead would drop the size().
	if call, ok := node.(*filter.CallNode); ok && call.Op == filter.OpSize {
		return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "size() is supported by Lua translator only as a comparison operand")
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	field, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field name, got %T", result)
	}

	return field, nil
}

// translateLogical handles && and || operators.
func (t *FilterTranslator) translateLogical(luaOp string, left, right filter.Node) (string, error) {
	l, err := t.getExprString(left)
	if err != nil {
		return "", err
	}

	r, err := t.getExprString(right)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("(%s %s %s)", l, luaOp, r), nil
}

// translateNot handles the ! operator with SQL's three-valued logic, as the evaluator and the SQL translators apply it: the
// negation of a predicate that is unknown for a record (an ordering comparison with a nil or differently typed value, a bare
// boolean that is nil, size() of a nil value, a nil string-function argument, a substring() that does not exist) does not
// match either. A Lua expression is true or false, so ! is not rendered as `not` around a clause: it is pushed down through &&
// and || (De Morgan) to each predicate, which renders its own negation — false where the predicate is unknown.
func (t *FilterTranslator) translateNot(operand filter.Node) (string, error) {
	return t.negate(operand)
}

// negate renders the negation of the predicate n ([FilterTranslator.translateNot]). It charges the depth guard for every node it
// renders itself, as the visitor does.
func (t *FilterTranslator) negate(n filter.Node) (string, error) {
	if t.predicateRenderer != nil {
		rendered, handled, err := t.predicateRenderer(n)
		if err != nil {
			return "", err
		}

		if handled {
			return fmt.Sprintf("(not %s)", rendered), nil
		}
	}

	switch node := n.(type) {
	case *filter.IdentNode:
		// A bare boolean holds when the value is true; its negation when it is false. A nil value is unknown, a value of another
		// type an evaluator error: neither matches.
		ref, err := t.valueString(node)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("(%s == %s)", ref, luaFalse), nil
	case *filter.UnaryOpNode:
		if node.Op != filter.OpNot {
			break
		}

		if err := t.depth.Enter(); err != nil {
			return "", err
		}
		defer t.depth.Leave()

		return t.getExprString(node.Operand)
	case *filter.BinaryOpNode:
		return t.negateBinary(node)
	case *filter.CallNode:
		return t.negateCall(node)
	}

	inner, err := t.getExprString(n)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("(not %s)", inner), nil
}

// negateBinary renders the negation of a binary operation: De Morgan through && and ||, the comparison's own negation otherwise.
func (t *FilterTranslator) negateBinary(node *filter.BinaryOpNode) (string, error) {
	if err := t.depth.Enter(); err != nil {
		return "", err
	}
	defer t.depth.Leave()

	switch node.Op {
	case filter.OpAnd, filter.OpOr:
		l, err := t.negate(node.Left)
		if err != nil {
			return "", err
		}

		r, err := t.negate(node.Right)
		if err != nil {
			return "", err
		}

		luaOp := "or"
		if node.Op == filter.OpOr {
			luaOp = "and"
		}

		return fmt.Sprintf("(%s %s %s)", l, luaOp, r), nil
	case filter.OpIn:
		return t.translateIn(node.Left, node.Right, true)
	default:
		return t.translateComparison(node.Op, node.Left, node.Right, true)
	}
}

// negateCall renders the negation of a function call ([FilterTranslator.translateCall]).
func (t *FilterTranslator) negateCall(node *filter.CallNode) (string, error) {
	if err := t.depth.Enter(); err != nil {
		return "", err
	}
	defer t.depth.Leave()

	result, err := t.translateCall(node, true)
	if err != nil {
		return "", err
	}

	s, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected expression string, got %T", result)
	}

	return s, nil
}

// getExprString accepts a node and ensures the result is an expression string.
func (t *FilterTranslator) getExprString(node filter.Node) (string, error) {
	if t.predicateRenderer != nil {
		if out, handled, err := t.predicateRenderer(node); handled || err != nil {
			return out, err
		}
	}

	if _, ok := node.(*filter.LiteralNode); ok {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "literal is not a predicate")
	}

	// A bare field is a boolean test. Lua truthiness would accept any non-nil, non-false value ("no", 0, a table), so it compares
	// with true, as the negated form compares with false ([FilterTranslator.negate]): a nil or non-bool value matches neither.
	if _, ok := node.(*filter.IdentNode); ok {
		ref, err := t.valueString(node)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("(%s == %s)", ref, luaTrue), nil
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	s, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected expression string, got %T", result)
	}

	return s, nil
}

// valueString renders a scalar operand without confusing field names with literals.
func (t *FilterTranslator) valueString(node filter.Node) (string, error) {
	switch value := node.(type) {
	case *filter.IdentNode:
		field, err := t.getFieldName(node)
		if err != nil {
			return "", err
		}

		return t.fieldRef(field), nil
	case *filter.LiteralNode:
		return t.formatLiteral(value.Value)
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "expected scalar operand, got %T", node)
	}
}

// visitor adapts a [FilterTranslator] to [filter.Visitor] for the walk. The visit methods stay unexported: they operate on the
// per-call state of one Translate run, so a caller invoking them directly on a shared translator would race with concurrent
// translations and bypass the field, limit and telemetry checks of a run. A visitor holds a single pointer, so converting it to
// the interface does not allocate.
type visitor struct{ t *FilterTranslator }

var _ filter.Visitor = visitor{}

func (v visitor) VisitLiteral(n *filter.LiteralNode) (any, error)   { return v.t.visitLiteral(n) }
func (v visitor) VisitIdent(n *filter.IdentNode) (any, error)       { return v.t.visitIdent(n) }
func (v visitor) VisitBinaryOp(n *filter.BinaryOpNode) (any, error) { return v.t.visitBinaryOp(n) }
func (v visitor) VisitUnaryOp(n *filter.UnaryOpNode) (any, error)   { return v.t.visitUnaryOp(n) }
func (v visitor) VisitCall(n *filter.CallNode) (any, error)         { return v.t.visitCall(n) }
func (v visitor) VisitList(n *filter.ListNode) (any, error)         { return v.t.visitList(n) }

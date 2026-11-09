// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"cel.dev/cel-go/common/operators"
	"github.com/shopspring/decimal"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	corestrings "github.com/altessa-s/go-atlas-core/text/strings"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// conversion is the state of converting one parsed expression: the shared [Parser] plus the source text and the code-point
// offset of every expression, from which float literals recover their spelling.
type conversion struct {
	*Parser
	source    []rune
	positions map[int64]int32
}

// convertExpr converts a CEL expression to our AST node.
func (p *conversion) convertExpr(expr *exprpb.Expr) (filter.Node, error) {
	if expr == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "nil expression")
	}

	switch e := expr.ExprKind.(type) {
	case *exprpb.Expr_ConstExpr:
		return p.convertConst(e.ConstExpr, expr.GetId())
	case *exprpb.Expr_IdentExpr:
		if !filter.IsFieldPath(e.IdentExpr.Name) {
			// A root-scoped identifier (.a) names no field.
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "invalid field path %q", e.IdentExpr.Name)
		}

		return &filter.IdentNode{Name: e.IdentExpr.Name}, nil
	case *exprpb.Expr_SelectExpr:
		return p.convertSelect(e.SelectExpr)
	case *exprpb.Expr_CallExpr:
		return p.convertCall(e.CallExpr)
	case *exprpb.Expr_ListExpr:
		return p.convertList(e.ListExpr)
	default:
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "unsupported expression type %T", e)
	}
}

// convertConst converts a CEL constant to a LiteralNode.
func (p *conversion) convertConst(c *exprpb.Constant, id int64) (filter.Node, error) {
	switch k := c.ConstantKind.(type) {
	case *exprpb.Constant_BoolValue:
		return &filter.LiteralNode{Value: k.BoolValue}, nil
	case *exprpb.Constant_Int64Value:
		return &filter.LiteralNode{Value: k.Int64Value}, nil
	case *exprpb.Constant_Uint64Value:
		return &filter.LiteralNode{Value: k.Uint64Value}, nil
	case *exprpb.Constant_DoubleValue:
		return &filter.LiteralNode{Value: k.DoubleValue, Text: p.floatSpelling(id, k.DoubleValue)}, nil
	case *exprpb.Constant_StringValue:
		return &filter.LiteralNode{Value: k.StringValue}, nil
	case *exprpb.Constant_BytesValue:
		return &filter.LiteralNode{Value: k.BytesValue}, nil
	case *exprpb.Constant_NullValue:
		return &filter.LiteralNode{Value: nil}, nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedType, "unsupported constant type %T", k)
	}
}

// unverifiedSpelling marks a float literal whose source spelling could not be recovered. It is not a decimal, so a
// [filter.FieldKindDecimal] operand rejects the literal instead of falling back to its float64 value.
const unverifiedSpelling = "<unrecovered float literal>"

// floatSpelling returns "" only when the shortest round-trip decimal of v is certified to be exactly the literal's source spelling
// (within the Decimal128 range and at most [MaxExactFloatDigits] digits). Otherwise it returns the spelling — which downstream
// decimal validation reads exactly or rejects — or [unverifiedSpelling] when the spelling cannot be recovered at all; it never lets
// a decimal operand fall back to the float. The literal starts at its recorded offset, with an optional minus sign the parser
// folds into the constant.
func (p *conversion) floatSpelling(id int64, v float64) string {
	pos, ok := p.positions[id]
	if !ok || pos < 0 || int(pos) >= len(p.source) {
		return unverifiedSpelling
	}

	src := p.source[pos:]

	var b strings.Builder
	if len(src) > 0 && src[0] == '-' {
		b.WriteRune('-')

		src = src[1:]
		for len(src) > 0 && unicode.IsSpace(src[0]) {
			src = src[1:]
		}
	}

	prev := rune(0)

	for _, r := range src {
		isDigit := r >= '0' && r <= '9'
		if !isDigit && r != '.' && r != 'e' && r != 'E' && ((r != '+' && r != '-') || (prev != 'e' && prev != 'E')) {
			break
		}

		b.WriteRune(r)
		prev = r
	}

	text := b.String()

	f, err := strconv.ParseFloat(text, 64)
	switch {
	case err != nil:
		if errors.Is(err, strconv.ErrRange) {
			return text // overflow or underflow: the float is not the value written
		}

		return unverifiedSpelling
	case f != v:
		return unverifiedSpelling // the scanned text is not this literal
	}

	parsed, err := decimal.NewFromString(text)
	if err != nil {
		return text // e.g. an exponent beyond int32 (1e-2147483649), which the float silently rounded to 0
	}
	// Bound and normalize before any decimal arithmetic: Equal on an unbounded value (0e-999999999) would rescale by its exponent.
	// A spelling outside the Decimal128 range is kept as is, so a decimal field rejects it.
	exact, err := filter.DecimalLiteral(parsed)
	if err != nil {
		return text
	}

	if short, err := filter.DecimalLiteral(v); err == nil && short.Equal(exact) {
		return ""
	}

	return text
}

// convertSelect converts a CEL select expression (field access) to an IdentNode.
// If TestOnly is true, this represents a has() macro call.
func (p *conversion) convertSelect(s *exprpb.Expr_Select) (filter.Node, error) {
	path, err := p.buildFieldPath(s)
	if err != nil {
		return nil, err
	}

	if s.TestOnly {
		return &filter.CallNode{
			Op:     filter.OpHas,
			Target: &filter.IdentNode{Name: path},
		}, nil
	}

	return &filter.IdentNode{Name: path}, nil
}

// buildFieldPath recursively builds a dotted field path from a select expression. Every selected segment must be a plain
// identifier: a backtick-escaped one (a.`b c`, a.`b.c`) is refused even when the joined path would read as plain
// identifiers, and so is a root-scoped operand (.a.b). Only the grammar orderby and projection accept names a field.
func (p *Parser) buildFieldPath(s *exprpb.Expr_Select) (string, error) {
	parts := make([]string, 0, 4) // Pre-allocate for typical depth

	operand := s.Operand
	for operand != nil {
		switch e := operand.ExprKind.(type) {
		case *exprpb.Expr_IdentExpr:
			parts = append([]string{e.IdentExpr.Name}, parts...)
			operand = nil
		case *exprpb.Expr_SelectExpr:
			parts = append([]string{e.SelectExpr.Field}, parts...)
			operand = e.SelectExpr.Operand
		default:
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "cannot build field path from %T", e)
		}
	}

	parts = append(parts, s.Field)

	path := corestrings.Join(parts, corestrings.JoinOptions{Separator: "."})
	for _, part := range parts {
		if strings.Contains(part, ".") || !filter.IsFieldPath(part) {
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "invalid field path %q", path)
		}
	}

	return path, nil
}

// convertCall converts a CEL call expression to the appropriate node type.
func (p *conversion) convertCall(c *exprpb.Expr_Call) (filter.Node, error) {
	if isUserFunction(c.Function) && p.allowedFunctions.Len() > 0 &&
		!p.allowedFunctions.Contains(c.Function) {
		return nil, coreerrs.Wrapf(filter.ErrFunctionNotAllowed, "%s", c.Function)
	}

	if p.customFunctions.Contains(c.Function) {
		return p.convertCustomFunction(c)
	}

	switch c.Function {
	// Comparison operators
	case operators.Equals:
		return p.convertBinaryOp(filter.OpEqual, c.Args)
	case operators.NotEquals:
		return p.convertBinaryOp(filter.OpNotEqual, c.Args)
	case operators.Less:
		return p.convertBinaryOp(filter.OpLT, c.Args)
	case operators.LessEquals:
		return p.convertBinaryOp(filter.OpLTE, c.Args)
	case operators.Greater:
		return p.convertBinaryOp(filter.OpGT, c.Args)
	case operators.GreaterEquals:
		return p.convertBinaryOp(filter.OpGTE, c.Args)

	// Logical operators
	case operators.LogicalAnd:
		return p.convertBinaryOp(filter.OpAnd, c.Args)
	case operators.LogicalOr:
		return p.convertBinaryOp(filter.OpOr, c.Args)
	case operators.LogicalNot:
		return p.convertUnaryOp(filter.OpNot, c.Args)

	// Membership
	case operators.In:
		return p.convertBinaryOp(filter.OpIn, c.Args)

	// String functions
	case "contains":
		return p.convertStringCall(filter.OpContains, c)
	case "startsWith":
		return p.convertStringCall(filter.OpStartsWith, c)
	case "endsWith":
		return p.convertStringCall(filter.OpEndsWith, c)
	case "matches":
		return p.convertMethodCall(filter.OpMatches, c.Target, c.Args)
	case "substring":
		return p.convertMethodCall(filter.OpSubstring, c.Target, c.Args)

	// Size function
	case "size":
		return p.convertSize(c)

	// Has macro
	case operators.Has:
		if len(c.Args) != 1 {
			return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "has() requires exactly one argument")
		}

		arg, err := p.convertExpr(c.Args[0])
		if err != nil {
			return nil, err
		}

		return &filter.CallNode{Op: filter.OpHas, Target: arg}, nil

	// Timestamp function
	case funcTimestamp:
		return p.convertTimestamp(c.Args)

	default:
		if p.customFunctions.Contains(c.Function) {
			return p.convertCustomFunction(c)
		}

		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%s", c.Function)
	}
}

// convertCustomFunction converts the arguments of a registered call
// and invokes its [filter.CustomFunction] handler. The handler's error is
// wrapped with [filter.ErrInvalidExpression] so callers can use [errors.Is].
// The caller must check [coremaps.ImmutableMap.Contains] first; this
// method panics if c.Function is not registered.
func (p *conversion) convertCustomFunction(c *exprpb.Expr_Call) (filter.Node, error) {
	h, _ := p.customFunctions.Get(c.Function)

	args := make([]filter.Node, 0, len(c.Args)+1)
	if c.Target != nil {
		target, err := p.convertExpr(c.Target)
		if err != nil {
			return nil, err
		}

		args = append(args, target)
	}

	for _, a := range c.Args {
		node, err := p.convertExpr(a)
		if err != nil {
			return nil, err
		}

		args = append(args, node)
	}

	out, err := h(args)
	if err != nil {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
			"custom function %q: %w", c.Function, err)
	}

	if out == nil {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
			"custom function %q returned nil node", c.Function)
	}
	// Snapshot handler-owned nodes before they enter the parser cache.
	return filter.Clone(out)
}

// convertTimestamp converts a timestamp() call to a LiteralNode with time.Time.
func (p *conversion) convertTimestamp(args []*exprpb.Expr) (filter.Node, error) {
	if len(args) != 1 {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "timestamp() requires exactly one argument")
	}

	arg, err := p.convertExpr(args[0])
	if err != nil {
		return nil, err
	}

	lit, ok := arg.(*filter.LiteralNode)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "timestamp() requires a string argument")
	}

	s, ok := lit.Value.(string)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "timestamp() requires a string argument")
	}

	t, err := p.timestampParser(s)
	if err != nil {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "invalid timestamp format: %w", err)
	}

	return &filter.LiteralNode{Value: t}, nil
}

// convertBinaryOp creates a BinaryOpNode from arguments.
func (p *conversion) convertBinaryOp(op filter.Operator, args []*exprpb.Expr) (filter.Node, error) {
	const binaryArgCount = 2
	if len(args) != binaryArgCount {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "binary operator requires 2 arguments, got %d", len(args))
	}

	left, err := p.convertExpr(args[0])
	if err != nil {
		return nil, err
	}

	right, err := p.convertExpr(args[1])
	if err != nil {
		return nil, err
	}

	return &filter.BinaryOpNode{Op: op, Left: left, Right: right}, nil
}

// convertUnaryOp creates a UnaryOpNode from arguments.
func (p *conversion) convertUnaryOp(op filter.Operator, args []*exprpb.Expr) (filter.Node, error) {
	if len(args) != 1 {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "unary operator requires 1 argument, got %d", len(args))
	}

	operand, err := p.convertExpr(args[0])
	if err != nil {
		return nil, err
	}

	return &filter.UnaryOpNode{Op: op, Operand: operand}, nil
}

// convertSize converts both CEL spellings of size — `field.size()` and the
// global `size(field)` — into the same node, with the measured operand as
// Target and no arguments. Translators and the Evaluator therefore see one
// shape regardless of how the expression was written.
func (p *conversion) convertSize(c *exprpb.Expr_Call) (filter.Node, error) {
	switch {
	case c.Target != nil && len(c.Args) == 0:
		return p.convertMethodCall(filter.OpSize, c.Target, nil)
	case c.Target == nil && len(c.Args) == 1:
		return p.convertMethodCall(filter.OpSize, c.Args[0], nil)
	default:
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "size() requires exactly one operand: field.size() or size(field)")
	}
}

// convertStringCall converts contains, startsWith and endsWith. The global spelling with two arguments — `contains(field, "x")` —
// becomes the method form `field.contains("x")`, as [conversion.convertSize] does for size(), so translators and the Evaluator see
// one shape. Any other spelling keeps its target and arguments as written.
func (p *conversion) convertStringCall(op filter.Operator, c *exprpb.Expr_Call) (filter.Node, error) {
	const globalArgs = 2
	if c.Target == nil && len(c.Args) == globalArgs {
		return p.convertMethodCall(op, c.Args[0], c.Args[1:])
	}

	return p.convertMethodCall(op, c.Target, c.Args)
}

// convertMethodCall creates a CallNode for method-style calls.
func (p *conversion) convertMethodCall(op filter.Operator, target *exprpb.Expr, args []*exprpb.Expr) (filter.Node, error) {
	var targetNode filter.Node

	if target != nil {
		var err error

		targetNode, err = p.convertExpr(target)
		if err != nil {
			return nil, err
		}
	}

	argNodes := make([]filter.Node, 0, len(args))
	for _, arg := range args {
		node, err := p.convertExpr(arg)
		if err != nil {
			return nil, err
		}

		argNodes = append(argNodes, node)
	}

	return &filter.CallNode{Op: op, Target: targetNode, Args: argNodes}, nil
}

// convertList converts a CEL list expression to a ListNode.
func (p *conversion) convertList(l *exprpb.Expr_CreateList) (filter.Node, error) {
	elements := make([]filter.Node, 0, len(l.Elements))
	for _, elem := range l.Elements {
		node, err := p.convertExpr(elem)
		if err != nil {
			return nil, err
		}

		elements = append(elements, node)
	}

	return &filter.ListNode{Elements: elements}, nil
}

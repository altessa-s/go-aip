// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// A program evaluates a compiled tree of expressions: everything that depends only on the filter and the schema (field
// declarations, zero values, literal operands, decimal operands, compiled patterns and structural errors) is resolved once per
// filter, so evaluating a record reads its values and compares them. Errors that the reference evaluator reported while walking
// the AST are kept and returned at the same point of the evaluation, after the same reads and budget charges.

// exprKind selects the evaluation of an [expr].
type exprKind uint8

const (
	exprInvalid exprKind = iota // err
	exprNot                     // left
	exprAnd                     // left, right
	exprOr                      // left, right
	exprIdent                   // ref
	exprCall                    // call
	exprCompare                 // compare
	exprIn                      // in
)

// expr is one compiled filter node. Concrete payloads keep the evaluation free of interface dispatch, so the per-record state
// never escapes to the heap.
type expr struct {
	err     error
	ref     *fieldRef
	left    *expr
	right   *expr
	call    *callExpr
	compare *compareExpr
	in      *inExpr
	kind    exprKind
}

// fieldRef is an identifier resolved against the schema.
type fieldRef struct {
	zero any // value of an absent optional field, and of a null operand
	name string
	info listing.FieldInfo
}

// callExpr is a function call on a field: has()/exists() or a string function.
type callExpr struct {
	err     error // structural error returned after the field is read (size(), arguments, operand)
	ref     *fieldRef
	matcher filter.Regex
	needle  string
	op      filter.Operator
}

// compareExpr is a comparison of a field, or of size() of a field, with a literal operand.
type compareExpr struct {
	right    any   // literal operand, converted to a decimal for a decimal field
	rightErr error // error of the operand, returned after the field is read
	ref      *fieldRef
	op       filter.Operator
	size     bool
}

// inExpr is a membership test: a literal in an array field, or a field in a literal list.
type inExpr struct {
	value    any   // literal of `value in arrayField`
	err      error // operand error of `value in arrayField`, returned before the field is read
	listErr  error // error of a non-list right operand, returned after the field is read
	ref      *fieldRef
	elements []inElement
	array    bool // `value in arrayField`
}

// inElement is one element of a literal list.
type inElement struct {
	literal      any   // the literal as written
	literalErr   error // its conversion error
	candidate    any   // the literal converted for the field (decimal operand, or the zero value of a null)
	candidateErr error
}

// compiler resolves an AST into an [expr] tree.
type compiler struct {
	schema   *listing.Schema
	policy   *filter.Policy
	matchers map[string]filter.Regex
	refs     map[string]*fieldRef
}

func (c *compiler) compile(node filter.Node) *expr {
	switch n := node.(type) {
	case *filter.UnaryOpNode:
		return &expr{kind: exprNot, left: c.compile(n.Operand)}
	case *filter.BinaryOpNode:
		switch n.Op {
		case filter.OpAnd:
			return &expr{kind: exprAnd, left: c.compile(n.Left), right: c.compile(n.Right)}
		case filter.OpOr:
			return &expr{kind: exprOr, left: c.compile(n.Left), right: c.compile(n.Right)}
		case filter.OpIn:
			return c.compileIn(n.Left, n.Right)
		default:
			return c.compileCompare(n.Op, n.Left, n.Right)
		}
	case *filter.IdentNode:
		return &expr{kind: exprIdent, ref: c.ref(n.Name)}
	case *filter.CallNode:
		return c.compileCall(n)
	default:
		return invalidExpr(malformed("unsupported filter node %T", node))
	}
}

func invalidExpr(err error) *expr { return &expr{kind: exprInvalid, err: err} }

// malformed reports an AST shape that filter.Validate rejects before compilation, so a program built by CompileEvaluator never
// returns it. It wraps filter.ErrInvalidExpression, as the default evaluator's structural errors do.
func malformed(format string, args ...any) error {
	return coreerrs.Wrapf(filter.ErrInvalidExpression, format, args...)
}

// ref resolves a field once per program. An undeclared field reads with the zero declaration, as Schema.Field returns it.
func (c *compiler) ref(name string) *fieldRef {
	if r, ok := c.refs[name]; ok {
		return r
	}

	info, _ := c.schema.FieldInfo(name)
	r := &fieldRef{name: name, info: info, zero: zeroValue(info)}
	c.refs[name] = r

	return r
}

func (c *compiler) compileCall(n *filter.CallNode) *expr {
	ident, ok := n.Target.(*filter.IdentNode)
	if !ok {
		return invalidExpr(malformed("function target is %T", n.Target))
	}

	x := &callExpr{op: n.Op, ref: c.ref(ident.Name)}

	switch {
	case n.Op == filter.OpHas || n.Op == filter.OpExists:
	case n.Op == filter.OpSize:
		x.err = malformed("size() is only valid in a comparison")
	case len(n.Args) != 1:
		x.err = malformed("string function requires one argument")
	default:
		arg, err := literal(n.Args[0])
		if err != nil {
			x.err = err
			break
		}

		needle, ok := arg.(string)
		if !ok {
			x.err = malformed("string function argument is %T", arg)
			break
		}

		x.needle, x.matcher = needle, c.matchers[needle]
	}

	return &expr{kind: exprCall, call: x}
}

func (c *compiler) compileCompare(op filter.Operator, left, right filter.Node) *expr {
	x := &compareExpr{op: op}

	if size, ok := left.(*filter.CallNode); ok && size.Op == filter.OpSize {
		ident, ok := size.Target.(*filter.IdentNode)
		if !ok {
			return invalidExpr(malformed("size() target is %T", size.Target))
		}

		x.size, x.ref = true, c.ref(ident.Name)
	} else {
		ident, ok := left.(*filter.IdentNode)
		if !ok {
			return invalidExpr(malformed("comparison field is %T", left))
		}

		x.ref = c.ref(ident.Name)
	}

	x.right, x.rightErr = literal(right)
	if x.rightErr == nil && !x.size && x.ref.info.Kind == filter.FieldKindDecimal && x.right != nil {
		x.right, x.rightErr = c.decimalOperand(right, x.right)
	}

	return &expr{kind: exprCompare, compare: x}
}

func (c *compiler) compileIn(left, right filter.Node) *expr {
	if ident, ok := right.(*filter.IdentNode); ok {
		if ref := c.ref(ident.Name); ref.info.Array {
			x := &inExpr{array: true, ref: ref}

			x.value, x.err = literal(left)
			if x.err == nil && ref.info.Kind == filter.FieldKindDecimal {
				x.value, x.err = c.decimalOperand(left, x.value)
			}

			return &expr{kind: exprIn, in: x}
		}
	}

	ident, ok := left.(*filter.IdentNode)
	if !ok {
		return invalidExpr(malformed("in field is %T", left))
	}

	x := &inExpr{ref: c.ref(ident.Name)}

	list, ok := right.(*filter.ListNode)
	if !ok {
		x.listErr = malformed("in list is %T", right)
		return &expr{kind: exprIn, in: x}
	}

	x.elements = make([]inElement, len(list.Elements))
	for i, element := range list.Elements {
		el := &x.elements[i]

		el.literal, el.literalErr = literal(element)

		el.candidate, el.candidateErr = el.literal, el.literalErr
		if el.candidateErr == nil && x.ref.info.Kind == filter.FieldKindDecimal && el.candidate != nil {
			el.candidate, el.candidateErr = c.decimalOperand(element, el.candidate)
		}

		if el.candidateErr == nil && !x.ref.info.Array && el.candidate == nil {
			el.candidate = x.ref.zero
		}
	}

	return &expr{kind: exprIn, in: x}
}

// decimalOperand converts a literal operand of a decimal field; the result is boxed whether or not it fails, as the reference
// evaluator assigned it.
func (c *compiler) decimalOperand(node filter.Node, value any) (any, error) {
	d, err := c.policy.DecimalOperand(node, value)
	return d, err
}

// literal returns the value of a literal operand or list.
func literal(node filter.Node) (any, error) {
	switch n := node.(type) {
	case *filter.LiteralNode:
		if n.Value == nil {
			return nil, nil //nolint:nilnil // the null literal is a value, not an error
		}

		return n.Value, nil
	case *filter.ListNode:
		values := make([]any, len(n.Elements))
		for i, element := range n.Elements {
			value, err := literal(element)
			if err != nil {
				return nil, err
			}

			values[i] = value
		}

		return values, nil
	default:
		return nil, malformed("filter operand is %T", node)
	}
}

// zeroValue returns the value an absent optional field reads as: the stored zero of an optional enum, else the zero of its kind.
func zeroValue(info listing.FieldInfo) any {
	if info.EnumZero != "" {
		return info.EnumZeroValue
	}

	switch info.Kind {
	case filter.FieldKindInt:
		return int64(0)
	case filter.FieldKindFloat:
		return float64(0)
	case filter.FieldKindString:
		return ""
	case filter.FieldKindBool:
		return false
	case filter.FieldKindBytes:
		return []byte{}
	case filter.FieldKindTimestamp:
		return time.Time{}
	case filter.FieldKindDecimal:
		return decimal.Zero
	default:
		return nil
	}
}

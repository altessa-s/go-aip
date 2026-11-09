// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype

import (
	"slices"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Dialect is the part of the type checker that differs between SQL dialects: field storage, literal values and the
// checks of one operator. F is the dialect's field descriptor, stored in [Expr.Field].
type Dialect[F any] interface {
	// Field resolves a field used as an operand: its descriptor, operand kind and whether it is an array. It refuses the
	// storage the dialect cannot compare.
	Field(name string) (F, sqlbase.Kind, bool, error)
	// Hint returns the kind a field suggests for the literal it is compared with.
	Hint(name string) (sqlbase.Kind, error)
	// InferKind returns the kind of a literal value without a hint.
	InferKind(v any) sqlbase.Kind
	// Literal validates a non-null literal as an operand of kind and returns the value to store in [Expr.Value].
	Literal(x *filter.LiteralNode, kind sqlbase.Kind) (any, error)
	// Comparison checks a binary operator over typed operands before the shared checks; nil accepts it.
	Comparison(op filter.Operator, a, b *Expr[F]) error
	// Regex checks the pattern of a matches() call e after the policy compiled it into e.Matcher.
	Regex(e *Expr[F], pattern string) error
}

// Checker types a complete filter tree against a schema before anything is rendered, so a malformed branch cannot bypass
// the type checks of another. A Checker holds per-call state: create one per filter.
type Checker[F any] struct {
	// Dialect resolves fields and validates literals.
	Dialect Dialect[F]
	// Policy compiles patterns and checks literals against enum and field declarations.
	Policy *filter.Policy
	// TypeError and Unsupported build the dialect's errors for incompatible operands and an unsupported expression.
	TypeError, Unsupported func() error
	// Contract accepts an array field against a list with `in` (any element in the list).
	Contract bool
	// MaxArrayDimensions bounds nested lists; zero accepts only flat lists, refusing an array element as unsupported.
	MaxArrayDimensions int
	// NormalizeDecimals reads a decimal operand of a binary operator through [filter.Policy.NormalizeDecimalOperand].
	NormalizeDecimals bool
	// SubstringArgFirst refuses a substring() argument of a string function before checking the function's operands.
	SubstringArgFirst bool
	// LiteralStringArgs requires a literal argument for every string function, not only for matches().
	LiteralStringArgs bool
}

// Check types node, which must be a boolean expression, and returns its typed tree. node must not be nil.
func (c *Checker[F]) Check(node filter.Node) (*Expr[F], error) {
	root, err := c.build(node, sqlbase.Unknown)
	if err != nil {
		return nil, err
	}

	if root.Kind != sqlbase.Boolean || root.Array {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "filter is not boolean")
	}

	return root, nil
}

func (c *Checker[F]) build(n filter.Node, hint sqlbase.Kind) (*Expr[F], error) {
	if n == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "missing operand")
	}

	e := &Expr[F]{Tag: n.Kind(), Node: n}
	switch x := n.(type) {
	case *filter.IdentNode:
		f, kind, array, err := c.Dialect.Field(x.Name)
		if err != nil {
			return nil, err
		}

		e.Kind, e.Array, e.Field = kind, array, f
	case *filter.LiteralNode:
		return c.literal(e, x, hint)
	case *filter.ListNode:
		return c.list(e, x, hint)
	case *filter.UnaryOpNode:
		if x.Op != filter.OpNot {
			return nil, c.Unsupported()
		}

		a, err := c.build(x.Operand, sqlbase.Boolean)
		if err != nil {
			return nil, err
		}

		if a.Kind != sqlbase.Boolean || a.Array {
			return nil, c.TypeError()
		}

		e.Op, e.Kind, e.Children = x.Op, sqlbase.Boolean, []*Expr[F]{a}
	case *filter.BinaryOpNode:
		return c.binary(e, x)
	case *filter.CallNode:
		return c.call(e, x)
	default:
		return nil, c.Unsupported()
	}

	return e, nil
}

func (c *Checker[F]) literal(e *Expr[F], x *filter.LiteralNode, hint sqlbase.Kind) (*Expr[F], error) {
	if x.Value == nil {
		e.Kind = hint
		return e, nil
	}

	if hint == sqlbase.Unknown {
		hint = c.Dialect.InferKind(x.Value)
	}

	if !literalFits(hint, c.Dialect.InferKind(x.Value)) {
		return nil, c.TypeError()
	}

	v, err := c.Dialect.Literal(x, hint)
	if err != nil {
		return nil, err
	}

	e.Kind, e.Value = hint, v

	return e, nil
}

// list types a list of operands of one comparable kind. With MaxArrayDimensions zero a nested list is refused; otherwise
// nested lists must share their dimensions.
func (c *Checker[F]) list(e *Expr[F], x *filter.ListNode, hint sqlbase.Kind) (*Expr[F], error) {
	e.Array, e.Kind = true, hint

	nested := c.MaxArrayDimensions > 0
	if nested {
		e.Dimensions = []Dimension{{Length: len(x.Elements), LowerBound: 1}}
	}

	for i, item := range x.Elements {
		childHint := e.Kind
		if literal, ok := item.(*filter.LiteralNode); ok && childHint == sqlbase.Integer && IsNumber(c.Dialect.InferKind(literal.Value)) {
			childHint = ComparisonKind(childHint, c.Dialect.InferKind(literal.Value))
		}

		child, err := c.build(item, childHint)
		if err != nil {
			return nil, err
		}

		if child.Array && !nested {
			return nil, c.Unsupported()
		}

		if !Compatible(e.Kind, child.Kind) {
			return nil, c.TypeError()
		}

		e.Kind = ComparisonKind(e.Kind, child.Kind)

		if nested {
			if err = c.dimensions(e, child, i == 0); err != nil {
				return nil, err
			}
		}

		e.Children = append(e.Children, child)
	}

	if len(e.Dimensions) > c.MaxArrayDimensions && nested {
		return nil, c.TypeError()
	}

	SetListKind(e, e.Kind)

	return e, nil
}

// dimensions adds the dimensions of the element child to the list e: every element of a nested list is a list of the
// same shape.
func (c *Checker[F]) dimensions(e, child *Expr[F], first bool) error {
	if !child.Array {
		if len(e.Dimensions) > 1 {
			return c.TypeError()
		}

		return nil
	}

	if child.Tag != filter.NodeKindList {
		return c.Unsupported()
	}

	if first {
		e.Dimensions = append(e.Dimensions, child.Dimensions...)
	} else if !slices.Equal(e.Dimensions[1:], child.Dimensions) {
		return c.TypeError()
	}

	return nil
}

func (c *Checker[F]) hint(n filter.Node) (sqlbase.Kind, error) {
	if id, ok := n.(*filter.IdentNode); ok {
		return c.Dialect.Hint(id.Name)
	}

	return sqlbase.Unknown, nil
}

func (c *Checker[F]) binary(e *Expr[F], x *filter.BinaryOpNode) (*Expr[F], error) {
	if c.NormalizeDecimals {
		left, err := c.Policy.NormalizeDecimalOperand(x.Right, x.Left)
		if err != nil {
			return nil, err
		}

		right, err := c.Policy.NormalizeDecimalOperand(x.Left, x.Right)
		if err != nil {
			return nil, err
		}

		cloned := *x
		cloned.Left, cloned.Right = left, right
		x = &cloned
	}

	// A field operand suggests its kind for the opposite literal.
	rightField, err := c.hint(x.Right)
	if err != nil {
		return nil, err
	}

	leftField, err := c.hint(x.Left)
	if err != nil {
		return nil, err
	}

	leftHint, rightHint := rightField, leftField
	if x.Op == filter.OpAnd || x.Op == filter.OpOr {
		leftHint, rightHint = sqlbase.Boolean, sqlbase.Boolean
	}

	a, err := c.build(x.Left, leftHint)
	if err != nil {
		return nil, err
	}

	if rightHint == sqlbase.Unknown {
		rightHint = a.Kind
	}

	b, err := c.build(x.Right, rightHint)
	if err != nil {
		return nil, err
	}

	if a.Kind == sqlbase.Unknown {
		a.Kind = b.Kind
	}

	if b.Kind == sqlbase.Unknown {
		b.Kind = a.Kind
	}

	if a.Kind == sqlbase.Unknown && (a.Array || b.Array) {
		// An array holding only unknown nulls, with nothing typed to compare, resolves as text.
		a.Kind, b.Kind = sqlbase.Text, sqlbase.Text
	}

	SetListKind(a, a.Kind)
	SetListKind(b, b.Kind)

	if err = c.Dialect.Comparison(x.Op, a, b); err != nil {
		return nil, err
	}

	if err = c.checkBinary(x.Op, a, b); err != nil {
		return nil, err
	}

	if err = c.checkFieldLiteral(x.Left, leftField, x.Right); err != nil {
		return nil, err
	}

	if err = c.checkFieldLiteral(x.Right, rightField, x.Left); err != nil {
		return nil, err
	}

	e.Op, e.Kind, e.Children = x.Op, sqlbase.Boolean, []*Expr[F]{a, b}

	return e, nil
}

func (c *Checker[F]) checkBinary(op filter.Operator, a, b *Expr[F]) error {
	if !Compatible(a.Kind, b.Kind) {
		return c.TypeError()
	}

	switch op {
	case filter.OpAnd, filter.OpOr:
		if a.Kind != sqlbase.Boolean || b.Kind != sqlbase.Boolean || a.Array || b.Array {
			return c.TypeError()
		}
	case filter.OpIn:
		// The listing contract also accepts an array field against a list: any element in the list.
		if !b.Array || (a.Array && !c.Contract) {
			return c.TypeError()
		}
	default:
		if !op.IsComparison() {
			return c.Unsupported()
		}

		if (a.IsNull() || b.IsNull()) && op != filter.OpEqual && op != filter.OpNotEqual {
			return c.Unsupported()
		}

		if b.Array && !a.Array && !a.IsNull() {
			return c.Unsupported()
		}
	}

	return nil
}

// checkFieldLiteral checks the operand literal compared with the field operand of kind against the policy's
// declarations. A JSON field takes any JSON value unless it declares enum values.
func (c *Checker[F]) checkFieldLiteral(field filter.Node, kind sqlbase.Kind, literal filter.Node) error {
	id, ok := field.(*filter.IdentNode)
	if !ok {
		return nil
	}

	_, enum := c.Policy.EnumValues(id.Name)
	if enum || kind != sqlbase.JSON {
		return c.Policy.CheckLiteralKind(id.Name, literal)
	}

	return nil
}

func (c *Checker[F]) call(e *Expr[F], x *filter.CallNode) (*Expr[F], error) {
	target, args := x.Target, x.Args
	if target == nil && len(args) > 0 {
		target, args = args[0], args[1:]
	}

	a, err := c.build(target, sqlbase.Unknown)
	if err != nil {
		return nil, err
	}

	if a.IsSubstring() {
		// substring() is a comparison operand only, as in the scalar translators.
		return nil, errSubstringOperand()
	}

	e.Op, e.Children = x.Op, []*Expr[F]{a}
	switch x.Op {
	case filter.OpSubstring:
		return c.substring(e, args)
	case filter.OpSize:
		sized := a.Array || a.Kind == sqlbase.Text || a.Kind == sqlbase.Bytes || a.Kind == sqlbase.JSON
		if len(args) != 0 || !sized {
			return nil, c.TypeError()
		}

		e.Kind = sqlbase.Integer
	case filter.OpHas, filter.OpExists:
		if len(args) != 0 || a.Tag != filter.NodeKindIdent {
			return nil, c.Unsupported()
		}

		e.Kind = sqlbase.Boolean
	case filter.OpContains, filter.OpStartsWith, filter.OpEndsWith, filter.OpMatches:
		if c.SubstringArgFirst && len(args) == 1 && isSubstringCall(args[0]) {
			return nil, errSubstringOperand()
		}

		return c.stringCall(e, args)
	default:
		return nil, c.Unsupported()
	}

	return e, nil
}

// substring types `field.substring(start[, end])`, a text value: the target is a text field that is not an array, the
// bounds are int literals (see [sqlbase.SubstringBounds]).
func (c *Checker[F]) substring(e *Expr[F], args []filter.Node) (*Expr[F], error) {
	target := e.Children[0]

	switch {
	case target.Tag != filter.NodeKindIdent:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() of a value that is not a field")
	case target.Array:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() of an array field")
	case target.Kind != sqlbase.Text:
		return nil, c.TypeError()
	}

	start, length, guard, err := sqlbase.SubstringBounds(args)
	if err != nil {
		return nil, err
	}

	e.Kind, e.Span = sqlbase.Text, Span{Start: start, Length: length, Guard: guard}

	return e, nil
}

// stringCall types a string function: a text target and one text argument, or a JSON target and JSON argument of
// contains().
func (c *Checker[F]) stringCall(e *Expr[F], args []filter.Node) (*Expr[F], error) {
	target := e.Children[0]

	if len(args) != 1 {
		return nil, c.TypeError()
	}

	if e.Op == filter.OpContains && target.Kind == sqlbase.JSON && !target.Array {
		b, err := c.build(args[0], sqlbase.JSON)
		if err != nil {
			return nil, err
		}

		if b.Kind != sqlbase.JSON || b.Array {
			return nil, c.TypeError()
		}

		e.Kind = sqlbase.Boolean
		e.Children = append(e.Children, b)

		return e, nil
	}

	if target.Kind != sqlbase.Text {
		return nil, c.TypeError()
	}

	b, err := c.build(args[0], sqlbase.Text)
	if err != nil {
		return nil, err
	}

	if b.Kind != sqlbase.Text || b.Array {
		return nil, c.TypeError()
	}

	if b.IsSubstring() {
		return nil, errSubstringOperand()
	}

	if c.LiteralStringArgs && b.Tag != filter.NodeKindLiteral {
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "a string function takes a literal argument")
	}

	if e.Op == filter.OpMatches {
		if err = c.matches(e, b); err != nil {
			return nil, err
		}
	}

	e.Kind = sqlbase.Boolean
	e.Children = append(e.Children, b)

	return e, nil
}

// matches compiles the literal pattern b of the matches() call e through the policy, then lets the dialect check it.
func (c *Checker[F]) matches(e, b *Expr[F]) error {
	if b.Tag != filter.NodeKindLiteral {
		return c.Unsupported()
	}

	pattern, ok := b.Value.(string)
	if !ok {
		return c.TypeError()
	}

	matcher, err := c.Policy.CompileRegex(pattern)
	if err != nil {
		return err
	}

	e.Matcher, e.RE2 = matcher, c.Policy.UsesRE2Regex()

	return c.Dialect.Regex(e, pattern)
}

// isSubstringCall reports whether n is a substring() call.
func isSubstringCall(n filter.Node) bool {
	call, ok := n.(*filter.CallNode)
	return ok && call.Op == filter.OpSubstring
}

func errSubstringOperand() error {
	return coreerrs.Wrap(filter.ErrUnsupportedOperation, "substring() outside a comparison operand")
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/regexanchor"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// evalValidator validates a filter for the evaluator with the rules of the MongoDB translator, so that a filter the translator
// rejects fails before any data is read.
type evalValidator struct {
	policy *filter.Policy
	schema *Schema
}

// validate checks node as the MongoDB translator does: decimal literals, ordered comparisons with null, the query operators, the
// filter contract and the expression forms, and the negations of presence fields. The policy's limits are checked by the caller.
func (v *evalValidator) validate(node filter.Node) error {
	if v.policy.HasFieldKind(filter.FieldKindDecimal) {
		if err := v.decimals(node); err != nil {
			return err
		}
	}

	if err := orderedNull(node); err != nil {
		return err
	}

	if err := v.predicate(node); err != nil {
		return err
	}

	if node == nil {
		return nil
	}

	if err := v.contract(node); err != nil {
		return err
	}

	return v.presenceNegations(node)
}

// decimals checks, as the MongoDB translator does, that every literal compared with a [filter.FieldKindDecimal] field is a decimal
// BSON Decimal128 represents ([Schema.ValidateDecimal]).
func (v *evalValidator) decimals(node filter.Node) error {
	var (
		failure error
		check   func(filter.Node)
	)

	check = func(n filter.Node) {
		if failure != nil {
			return
		}

		switch n := n.(type) {
		case *filter.ListNode:
			for _, element := range n.Elements {
				check(element)
			}
		case *filter.LiteralNode:
			if n.Value == nil {
				return
			}

			value, err := v.policy.DecimalOperand(n, n.Value)
			if err == nil {
				err = v.schema.ValidateDecimal(value)
			}

			failure = err
		}
	}
	filter.Walk(node, func(n filter.Node) bool {
		if b, ok := n.(*filter.BinaryOpNode); ok {
			if field, ok := b.Left.(*filter.IdentNode); ok && v.policy.FieldKind(field.Name) == filter.FieldKindDecimal {
				check(b.Right)
			}

			if field, ok := b.Right.(*filter.IdentNode); ok && v.policy.FieldKind(field.Name) == filter.FieldKindDecimal {
				check(b.Left)
			}
		}

		return failure == nil
	})

	return failure
}

func (v *evalValidator) predicate(node filter.Node) error {
	switch n := node.(type) {
	case *filter.BinaryOpNode:
		switch n.Op {
		case filter.OpAnd, filter.OpOr:
			if err := v.predicate(n.Left); err != nil {
				return err
			}

			return v.predicate(n.Right)
		case filter.OpIn:
			return nil
		default:
			return v.comparison(n)
		}
	case *filter.UnaryOpNode:
		return v.predicate(n.Operand)
	case *filter.CallNode:
		if n.Op != filter.OpMatches {
			return nil
		}

		return matchesPattern(n)
	default:
		// Identifiers and literals need no MongoDB-specific check.
		return nil
	}
}

// comparison checks a comparison the MongoDB translator renders as a query operator: ordered bytes and, for
// size(field) on the left, the size operand.
func (v *evalValidator) comparison(n *filter.BinaryOpNode) error {
	if needsExpr(n.Left, n.Right) {
		return nil // checked by contract
	}

	if err := v.orderedBytes(n); err != nil {
		return err
	}

	call, ok := n.Left.(*filter.CallNode)
	if !ok || call.Op != filter.OpSize {
		return nil
	}

	if _, nested := call.Target.(*filter.CallNode); nested {
		return filter.ErrUnsupportedOperation
	}

	if err := filter.CheckFieldReference(call.Target); err != nil {
		return err
	}

	field, ok := call.Target.(*filter.IdentNode)
	if !ok || field == nil {
		return filter.ErrInvalidExpression
	}

	literal, ok := n.Right.(*filter.LiteralNode)
	if !ok || literal == nil {
		return filter.ErrInvalidExpression
	}

	return CheckSizeOperand(field.Name, literal.Value)
}

// matchesPattern checks that the pattern of a matches() call is a string literal MongoDB can anchor at end of text.
func matchesPattern(n *filter.CallNode) error {
	if len(n.Args) != 1 {
		return filter.ErrInvalidExpression
	}

	literal, ok := n.Args[0].(*filter.LiteralNode)
	if !ok || literal == nil {
		return coreerrs.Wrap(filter.ErrUnsupportedOperation, "matches() pattern must be a literal")
	}

	pattern, ok := literal.Value.(string)
	if !ok {
		return filter.ErrInvalidExpression
	}

	_, err := regexanchor.EndOfText(pattern)

	return err
}

// contract checks node as the MongoDB translator does: the predicates it renders as query documents by the backend-neutral filter
// contract ([filter.Validate]), and those it renders as aggregation expressions (field operands, a list of in holding a field, a
// string function with a field argument, substring()) by the rules of its expression forms.
func (v *evalValidator) contract(node filter.Node) error {
	switch n := node.(type) {
	case *filter.BinaryOpNode:
		if n == nil {
			break
		}

		switch n.Op {
		case filter.OpAnd, filter.OpOr:
			if err := v.child(n.Left); err != nil {
				return err
			}

			return v.child(n.Right)
		case filter.OpIn:
			return v.in(n)
		default:
			if needsExpr(n.Left, n.Right) {
				return v.exprComparison(n.Op, n.Left, n.Right)
			}
		}
	case *filter.UnaryOpNode:
		if n != nil && n.Op == filter.OpNot {
			return v.child(n.Operand)
		}
	case *filter.CallNode:
		if n != nil && isStringExprCall(n) {
			return v.stringExpr(n)
		}
	}

	return filter.Validate(node, v.policy)
}

// child is [evalValidator.contract] of an operand of a logical operator, which must be present.
func (v *evalValidator) child(node filter.Node) error {
	if nilcheck.IsNil(node) {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "nil AST node")
	}

	return v.contract(node)
}

// in checks `left in right`: `x in tags` on an array field with a scalar field or a substring() x is the expression equality
// tags == x; a list holding a field, or a substring() on the left, is the disjunction of a query in of the literal elements and an
// equality per other element.
func (v *evalValidator) in(n *filter.BinaryOpNode) error {
	if field, ok := n.Right.(*filter.IdentNode); ok && field != nil && v.policy.IsArrayField(field.Name) && needsExpr(n.Right, n.Left) &&
		(!filter.IsFieldReference(n.Left) || v.isScalarField(n.Left)) {
		return v.exprComparison(filter.OpEqual, n.Right, n.Left)
	}

	list, ok := n.Right.(*filter.ListNode)
	if !ok || list == nil || (!hasDynamicElement(list) && !isSubstring(n.Left)) {
		return filter.Validate(n, v.policy)
	}

	substring := isSubstring(n.Left)

	var literals []filter.Node

	for _, element := range list.Elements {
		if _, isLiteral := element.(*filter.LiteralNode); isLiteral && !substring {
			literals = append(literals, element)
			continue
		}

		if err := v.contract(&filter.BinaryOpNode{Op: filter.OpEqual, Left: n.Left, Right: element}); err != nil {
			return err
		}
	}

	if len(literals) == 0 {
		return nil
	}

	return filter.Validate(&filter.BinaryOpNode{Op: filter.OpIn, Left: n.Left, Right: &filter.ListNode{Elements: literals}}, v.policy)
}

// isScalarField reports whether n is a field not declared by [filter.WithArrayFields].
func (v *evalValidator) isScalarField(n filter.Node) bool {
	ident, ok := n.(*filter.IdentNode)
	return ok && ident != nil && !v.policy.IsArrayField(ident.Name)
}

// presenceNegations rejects, as the MongoDB translator does, a negated `field in x` on a [WithSchemaPresenceFields] field
// whose x is not a list (an array field): the translator, which reads the filter in negation normal form, has no form for it.
func (v *evalValidator) presenceNegations(node filter.Node) error {
	if !v.schema.HasPresenceFields() {
		return nil
	}

	semantics := v.schema
	presence := semantics.PresenceFields()

	return firstError(filter.NegationNormalForm(node, v.policy.ArrayFields(), presence), func(n filter.Node) error {
		not, ok := n.(*filter.UnaryOpNode)
		if !ok || not.Op != filter.OpNot {
			return nil
		}

		in, ok := not.Operand.(*filter.BinaryOpNode)
		if !ok || in.Op != filter.OpIn {
			return nil
		}

		ident, ok := in.Left.(*filter.IdentNode)
		if _, isList := in.Right.(*filter.ListNode); ok && !isList && semantics.IsPresenceField(ident.Name) {
			return coreerrs.Wrapf(filter.ErrInvalidExpression, "negated in of presence field %s requires a list", ident.Name)
		}

		return nil
	})
}

// orderedBytes rejects, as the MongoDB translator does, an ordered comparison of a bytes field or with a bytes literal in the query
// form with [filter.ErrUnsupportedOperation]: MongoDB orders BinData by length before its bytes, which CEL orders lexicographically.
func (v *evalValidator) orderedBytes(n *filter.BinaryOpNode) error {
	if !isOrdering(n.Op) || isSizeCall(n.Left) {
		return nil
	}

	ident, _ := n.Left.(*filter.IdentNode)

	lit, _ := n.Right.(*filter.LiteralNode)
	if _, isBytes := literalValue(lit).([]byte); isBytes || (ident != nil && v.policy.FieldKind(ident.Name) == filter.FieldKindBytes) {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v of bytes: MongoDB orders BinData by length first", n.Op)
	}

	return nil
}

// literalValue is the value of lit, nil for a nil literal.
func literalValue(lit *filter.LiteralNode) any {
	if lit == nil {
		return nil
	}

	return lit.Value
}

// orderedNull rejects an ordered comparison (<, <=, >, >=) with the null literal, which no backend defines, with
// [filter.ErrUnsupportedOperation].
func orderedNull(node filter.Node) error {
	return firstError(node, func(n filter.Node) error {
		if b, ok := n.(*filter.BinaryOpNode); ok && isOrdering(b.Op) && (isNullLiteral(b.Left) || isNullLiteral(b.Right)) {
			return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v with null", b.Op)
		}

		return nil
	})
}

// firstError calls check on node and every node below it, depth first ([filter.Walk]), and returns the first error. The filter
// is within the policy's limits, which reject a nil node inside it.
func firstError(node filter.Node, check func(filter.Node) error) error {
	var failure error

	filter.Walk(node, func(n filter.Node) bool {
		failure = check(n)
		return failure == nil
	})

	return failure
}

// isOrdering reports whether op is <, <=, > or >=.
func isOrdering(op filter.Operator) bool {
	return op == filter.OpLT || op == filter.OpLTE || op == filter.OpGT || op == filter.OpGTE
}

// isNullLiteral reports whether n is the null literal.
func isNullLiteral(n filter.Node) bool {
	lit, ok := n.(*filter.LiteralNode)
	return ok && lit != nil && lit.Value == nil
}

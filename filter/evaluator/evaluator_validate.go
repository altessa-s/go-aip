// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Validate checks the complete expression without reading data. Limits, field
// references and the allow-list apply in every mode, including skipped branches.
// Every check the options make decidable without data runs here, as a translator
// runs it: literal kinds and enum values, decimal literals, substring() literal
// bounds, a field compared with a field of an incompatible declared kind, `x in`
// a field declared with a scalar kind, an ordered comparison with null and a
// literal in the place of a predicate. Under WithProfile the profile's compiler
// validates instead. Data-dependent errors remain evaluation errors.
func (e *Evaluator) Validate(node filter.Node) error {
	if e.config.compiler != nil {
		_, err := e.config.compiler.CompileEvaluator(e.config.Policy, node)
		return err
	}

	if err := e.config.CheckFields(node); err != nil || node == nil {
		return err
	}

	if err := orderedNull(node); err != nil {
		return err
	}

	return (&staticValidator{config: e.config}).validate(node)
}

// orderedNull rejects an ordered comparison (<, <=, >, >=) with the null literal, which no backend defines, with
// [filter.ErrUnsupportedOperation].
func orderedNull(node filter.Node) error {
	return eachNode(node, func(n filter.Node) error {
		if b, ok := n.(*filter.BinaryOpNode); ok && isOrdering(b.Op) && (isNullLiteral(b.Left) || isNullLiteral(b.Right)) {
			return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison %v with null", b.Op)
		}

		return nil
	})
}

// eachNode calls check on node and every node below it, depth first, and returns the first error. It recurses through the node
// types of the filter AST without the closures of [filter.Walk], so a check costs no allocation; a nil node is skipped.
func eachNode(node filter.Node, check func(filter.Node) error) error {
	if nilcheck.IsNil(node) {
		return nil
	}

	if err := check(node); err != nil {
		return err
	}

	switch n := node.(type) {
	case *filter.BinaryOpNode:
		if err := eachNode(n.Left, check); err != nil {
			return err
		}

		return eachNode(n.Right, check)
	case *filter.UnaryOpNode:
		return eachNode(n.Operand, check)
	case *filter.CallNode:
		if err := eachNode(n.Target, check); err != nil {
			return err
		}

		for _, arg := range n.Args {
			if err := eachNode(arg, check); err != nil {
				return err
			}
		}
	case *filter.ListNode:
		for _, element := range n.Elements {
			if err := eachNode(element, check); err != nil {
				return err
			}
		}
	}

	return nil
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

// staticValidator runs, for the default evaluator, the checks the evaluation would make on every record but which do not depend on
// the data, so an expression a translator rejects fails in Validate and Prepare rather than per record (and an empty data set
// cannot hide it).
type staticValidator struct {
	config *executionConfig
}

// validate checks node, a predicate, and every substring() call in it.
func (v *staticValidator) validate(node filter.Node) error {
	if err := v.predicate(node); err != nil {
		return err
	}

	return eachNode(node, func(n filter.Node) error {
		if call, ok := n.(*filter.CallNode); ok && call.Op == filter.OpSubstring {
			return substringLiteralBounds(call.Args)
		}

		return nil
	})
}

// predicate checks a node in the place of a predicate: a literal or a list is none.
func (v *staticValidator) predicate(node filter.Node) error {
	switch n := node.(type) {
	case *filter.LiteralNode, *filter.ListNode:
		return coreerrs.Wrapf(filter.ErrInvalidExpression, "expected a predicate, got %T", node)
	case *filter.BinaryOpNode:
		switch n.Op {
		case filter.OpAnd, filter.OpOr:
			if err := v.predicate(n.Left); err != nil {
				return err
			}

			return v.predicate(n.Right)
		case filter.OpIn:
			return v.in(n.Left, n.Right)
		default:
			return v.comparison(n.Left, n.Right)
		}
	case *filter.UnaryOpNode:
		if n.Op == filter.OpNot {
			return v.predicate(n.Operand)
		}
	}

	return nil
}

// comparison checks the literal operand of a field against its declared kind and enum values, and two field operands against
// each other's declared kinds.
func (v *staticValidator) comparison(left, right filter.Node) error {
	if err := v.config.CheckComparison(left, right); err != nil {
		return err
	}

	return v.fieldKinds(left, right)
}

// fieldKinds rejects two field operands whose declared kinds can never compare equal with [filter.ErrFieldTypeMismatch].
func (v *staticValidator) fieldKinds(left, right filter.Node) error {
	l, lok := left.(*filter.IdentNode)
	r, rok := right.(*filter.IdentNode)

	if !lok || !rok || l == nil || r == nil {
		return nil
	}

	a, b := v.config.FieldKind(l.Name), v.config.FieldKind(r.Name)
	if filter.KindsComparable(a, b) {
		return nil
	}

	return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "cannot compare %s (%s) with %s (%s)", l.Name, a, r.Name, b)
}

// in checks `left in right`: `x in field` on an array field is the comparison field == x; a field declared with a scalar kind is no
// list ([filter.ErrInvalidExpression]); the literal elements of a list are checked against the field's kind and enum values, and its
// field elements against the field's kind.
func (v *staticValidator) in(left, right filter.Node) error {
	if field, ok := right.(*filter.IdentNode); ok && field != nil {
		if v.config.IsArrayField(field.Name) {
			if !filter.IsFieldReference(left) {
				return v.comparison(right, left)
			}

			return nil
		}

		if v.config.FieldKind(field.Name) != filter.FieldKindUnspecified {
			return coreerrs.Wrapf(filter.ErrInvalidExpression, "in requires a list, but %s is a %s field", field.Name, v.config.FieldKind(field.Name))
		}
	}

	ident, ok := left.(*filter.IdentNode)
	if !ok || ident == nil {
		return nil
	}

	if err := v.config.CheckLiteralKind(ident.Name, right); err != nil {
		return err
	}

	if list, ok := right.(*filter.ListNode); ok && list != nil {
		for _, element := range list.Elements {
			if err := v.fieldKinds(left, element); err != nil {
				return err
			}
		}
	}

	return nil
}

// substringLiteralBounds checks the bounds of substring() that are literals: one or two, integers, not negative, start not after end.
func substringLiteralBounds(args []filter.Node) error {
	if len(args) < 1 || len(args) > 2 {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	var bounds []int64

	for _, arg := range args {
		lit, ok := arg.(*filter.LiteralNode)
		if !ok || lit == nil {
			continue
		}

		bound, ok := lit.Value.(int64)
		if !ok {
			return coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bound must be an int, got %T", lit.Value)
		}

		if bound < 0 {
			return coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() bound %d out of range", bound)
		}

		bounds = append(bounds, bound)
	}

	if len(bounds) == 2 && bounds[0] > bounds[1] {
		return coreerrs.Wrapf(filter.ErrInvalidExpression, "substring(%d, %d): start after end", bounds[0], bounds[1])
	}

	return nil
}

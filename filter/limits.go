// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// CheckLimits bounds a complete translation before any output is built. Every
// node consumes an operation; compound nodes, including lists, consume depth.
// A nil root is an absent filter and consumes no budget. Validation visits
// both branches of a logical expression, even when evaluation skips one. It also applies
// the matches() policy to a literal pattern: the safe subset unless
// [WithUnsafeRegex], and under [WithUntrustedInput] [WithSafeRegex].
func (o *Policy) CheckLimits(root Node) error {
	if root == nil {
		return nil
	}

	remaining := o.maxOperations

	return o.checkNodeLimits(root, 0, &remaining, false)
}

// CheckFields checks every field reference and the allow-list, including branches
// skipped at evaluation time. Traversal is bounded by the policy's limits.
func (o *Policy) CheckFields(root Node) error {
	if root == nil {
		return nil
	}

	remaining := o.maxOperations

	return o.checkNodeLimits(root, 0, &remaining, true)
}

func (o *Policy) checkNodeLimits(node Node, depth int, remaining *int, fields bool) error {
	if nilcheck.IsNil(node) {
		return coreerrs.Wrap(ErrInvalidExpression, "nil AST node")
	}

	if *remaining == 0 {
		return ErrMaxOperationsExceeded
	}

	*remaining--

	switch n := node.(type) {
	case *IdentNode:
		if fields {
			if err := CheckFieldReference(n); err != nil {
				return err
			}

			if !o.IsFieldAllowed(n.Name) {
				return coreerrs.Wrapf(ErrFieldNotAllowed, "%s", n.Name)
			}
		}

		return nil
	case *LiteralNode:
		return nil
	}

	if depth >= o.maxDepth {
		return ErrMaxDepthExceeded
	}
	// The built-in nodes are walked through their fields: ranging over Children() here, in a recursive function, moves the
	// iterator state and the loop body to the heap on every compound node. Children() is read only for a custom Node.
	switch n := node.(type) {
	case *BinaryOpNode:
		return o.checkChildLimits(depth, remaining, fields, true, n.Left, n.Right)
	case *UnaryOpNode:
		return o.checkChildLimits(depth, remaining, fields, true, n.Operand)
	case *CallNode:
		if err := o.checkRegexCall(n); err != nil {
			return err
		}

		if err := o.checkChildLimits(depth, remaining, fields, true, n.Target); err != nil {
			return err
		}

		return o.checkChildLimits(depth, remaining, fields, false, n.Args...)
	case *ListNode:
		return o.checkChildLimits(depth, remaining, fields, false, n.Elements...)
	default:
		return o.checkCustomNodeLimits(node, depth, remaining, fields)
	}
}

// checkCustomNodeLimits checks the children of a custom [Node] through Children(). It is a function of its own so that the
// iterator state it moves to the heap is allocated only for a custom node, not on every call of checkNodeLimits.
func (o *Policy) checkCustomNodeLimits(node Node, depth int, remaining *int, fields bool) error {
	for child := range node.Children() {
		if err := o.checkNodeLimits(child, depth+1, remaining, fields); err != nil {
			return err
		}
	}

	return nil
}

// checkChildLimits checks the children of a node at depth. With skipNil it skips a nil child, as Children() skips a nil
// operand of a binary or unary node and a nil call target; a nil list element or call argument is checked, and fails.
func (o *Policy) checkChildLimits(depth int, remaining *int, fields bool, skipNil bool, children ...Node) error {
	for _, child := range children {
		if skipNil && child == nil {
			continue
		}

		if err := o.checkNodeLimits(child, depth+1, remaining, fields); err != nil {
			return err
		}
	}

	return nil
}

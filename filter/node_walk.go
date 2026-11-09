// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"iter"
	"slices"
)

// Walk traverses the AST in depth-first order, calling fn for each node.
// If fn returns false, traversal stops: no further node is visited.
func Walk(root Node, fn func(Node) bool) {
	walk(root, fn)
}

// walk is Walk reporting whether traversal may continue, so a stop requested
// deep in the tree also ends the traversal of every ancestor's later children.
//
// The built-in nodes are walked through their fields, in the order and with the nil-skipping of their Children: ranging over
// Children() in a recursive function moves the iterator and the loop body to the heap on every compound node. Children() is read
// only for a custom node ([walkCustom]).
func walk(n Node, fn func(Node) bool) bool {
	if n == nil {
		return true
	}

	if !fn(n) {
		return false
	}

	switch x := n.(type) {
	case *LiteralNode, *IdentNode:
		return true
	case *BinaryOpNode:
		return (x.Left == nil || walk(x.Left, fn)) && (x.Right == nil || walk(x.Right, fn))
	case *UnaryOpNode:
		return x.Operand == nil || walk(x.Operand, fn)
	case *CallNode:
		return (x.Target == nil || walk(x.Target, fn)) && walkAll(x.Args, fn)
	case *ListNode:
		return walkAll(x.Elements, fn)
	default:
		return walkCustom(n, fn)
	}
}

// walkAll walks nodes in order; a nil element is yielded by Children and skipped by walk.
func walkAll(nodes []Node, fn func(Node) bool) bool {
	for _, node := range nodes {
		if !walk(node, fn) {
			return false
		}
	}

	return true
}

// walkCustom walks the children of a custom node, read through Children(). They are collected first, so that fn is never
// captured by the iterator's loop body: a closure handed to an unknown iterator escapes, and fn with it, which would move every
// caller's fn to the heap although only a custom node needs the iterator.
func walkCustom(n Node, fn func(Node) bool) bool {
	return walkAll(slices.Collect(n.Children()), fn)
}

// AllNodes returns an iterator over all nodes in the AST in depth-first order.
func AllNodes(root Node) iter.Seq[Node] {
	return func(yield func(Node) bool) {
		Walk(root, yield)
	}
}

// CountNodes returns the total number of nodes in the AST.
func CountNodes(root Node) int {
	count := 0

	Walk(root, func(Node) bool {
		count++
		return true
	})

	return count
}

// Depth returns the maximum depth of the AST.
func Depth(root Node) int {
	if root == nil {
		return 0
	}

	children := 0

	switch x := root.(type) {
	case *LiteralNode, *IdentNode:
	case *BinaryOpNode:
		children = max(Depth(x.Left), Depth(x.Right))
	case *UnaryOpNode:
		children = Depth(x.Operand)
	case *CallNode:
		children = max(Depth(x.Target), maxDepth(x.Args))
	case *ListNode:
		children = maxDepth(x.Elements)
	default:
		children = customDepth(root)
	}

	return children + 1
}

// maxDepth returns the greatest [Depth] of nodes.
func maxDepth(nodes []Node) int {
	deepest := 0
	for _, node := range nodes {
		deepest = max(deepest, Depth(node))
	}

	return deepest
}

// customDepth returns the greatest [Depth] of the children of a custom node, read through Children() in a function of its own
// so that only a custom node pays for the iterator.
func customDepth(n Node) int {
	deepest := 0
	for child := range n.Children() {
		deepest = max(deepest, Depth(child))
	}

	return deepest
}

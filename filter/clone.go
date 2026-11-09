// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"reflect"
	"slices"
	"time"

	"github.com/altessa-s/go-aip/internal/literalvalue"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Clone copies the built-in AST and mutable byte literals. Unknown custom
// nodes and mutable literal types fail explicitly instead of aliasing the cache.
func Clone(root Node) (Node, error) {
	return cloneNode(root, make(map[Node]Node))
}

//nolint:nilnil // nil child: an absent call target
func cloneNode(node Node, copies map[Node]Node) (Node, error) {
	if node == nil {
		return nil, nil
	}

	switch n := node.(type) {
	case *LiteralNode:
		if n == nil {
			return nil, nilNodeError(node)
		}

		value, err := cloneLiteral(n.Value)
		if err != nil {
			return nil, err
		}

		return &LiteralNode{Value: value, Text: n.Text}, nil
	case *IdentNode:
		if n == nil {
			return nil, nilNodeError(node)
		}

		return &IdentNode{Name: n.Name}, nil
	case *BinaryOpNode, *UnaryOpNode, *CallNode, *ListNode:
		return cloneComposite(node, copies)
	}

	extension, ok := node.(ExtensionNode)
	if !ok {
		return nil, coreerrs.Wrapf(ErrInvalidExpression, "cannot copy custom node %T", node)
	}

	lowered, err := extension.Lower()
	if err != nil {
		return nil, err
	}

	if reflect.TypeOf(node).Comparable() && lowered == node {
		return nil, coreerrs.Wrap(ErrInvalidExpression, "extension lowered to itself")
	}

	return cloneNode(lowered, copies)
}

// cloneComposite copies a node with children. A node reached twice is copied once: the copy is recorded in copies before
// its children, so the copied tree shares exactly the nodes the original shares. A typed nil node is never recorded.
func cloneComposite(node Node, copies map[Node]Node) (Node, error) {
	if previous, ok := copies[node]; ok {
		return previous, nil
	}

	var err error

	switch n := node.(type) {
	case *BinaryOpNode:
		if n == nil {
			return nil, nilNodeError(node)
		}

		out := &BinaryOpNode{Op: n.Op}
		copies[node] = out

		out.Left, err = cloneNode(n.Left, copies)
		if err == nil {
			out.Right, err = cloneNode(n.Right, copies)
		}

		return out, err
	case *UnaryOpNode:
		if n == nil {
			return nil, nilNodeError(node)
		}

		out := &UnaryOpNode{Op: n.Op}
		copies[node] = out
		out.Operand, err = cloneNode(n.Operand, copies)

		return out, err
	case *CallNode:
		if n == nil {
			return nil, nilNodeError(node)
		}

		out := &CallNode{Op: n.Op}
		copies[node] = out

		out.Target, err = cloneNode(n.Target, copies)
		if err == nil {
			out.Args, err = cloneNodes(n.Args, copies)
		}

		return out, err
	default:
		list, _ := node.(*ListNode) // cloneNode passes only the four composite node types
		if list == nil {
			return nil, nilNodeError(node)
		}

		out := &ListNode{}
		copies[node] = out
		out.Elements, err = cloneNodes(list.Elements, copies)

		return out, err
	}
}

func cloneNodes(nodes []Node, copies map[Node]Node) ([]Node, error) {
	if nodes == nil {
		return nil, nil
	}

	out := make([]Node, len(nodes))
	for i, node := range nodes {
		var err error

		out[i], err = cloneNode(node, copies)
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

func cloneLiteral(value any) (any, error) {
	if v, ok := value.([]byte); ok {
		return slices.Clone(v), nil
	}

	if err := checkLiteral(value); err != nil {
		return nil, err
	}

	return value, nil
}

// checkLiteral returns [ErrUnsupportedType] unless value is a literal [Clone] can copy: nil, a time, bytes or an immutable value.
func checkLiteral(value any) error {
	switch value.(type) {
	case nil, time.Time, []byte, string, bool, int64, uint64, float64:
		return nil
	}

	if literalvalue.Immutable(value) {
		return nil
	}

	return coreerrs.Wrapf(ErrUnsupportedType, "cannot copy literal %T", value)
}

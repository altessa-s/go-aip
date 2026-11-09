// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"slices"

	"github.com/altessa-s/go-aip/internal/literalvalue"

	filter "github.com/altessa-s/go-aip/filter"
)

// clonePlan sizes a slab copy of a cached AST: the number of nodes of each built-in kind and of child slots it holds.
// [Parser.Parse] copies a cache hit through it into one backing array per node kind, a handful of allocations instead of
// one per node, and the copy is as independent of the cache as one made by [filter.Clone].
type clonePlan struct {
	binary, unary, call, list, ident, literal, children int
}

// newClonePlan returns the plan of root, or nil when only [filter.Clone] may copy it: the tree holds a custom node, a
// typed nil node, a node reachable twice, or a literal value that is neither immutable nor a byte slice.
func newClonePlan(root filter.Node) *clonePlan {
	p := &clonePlan{}
	seen := make(map[filter.Node]struct{})

	if !p.add(root, seen) {
		return nil
	}

	return p
}

//nolint:gocyclo // one case per built-in node kind
func (p *clonePlan) add(n filter.Node, seen map[filter.Node]struct{}) bool {
	if n == nil {
		return true
	}

	if _, dup := seen[n]; dup {
		return false
	}

	seen[n] = struct{}{}

	switch n := n.(type) {
	case *filter.LiteralNode:
		if n == nil {
			return false
		}

		if _, ok := n.Value.([]byte); !ok && !literalvalue.Immutable(n.Value) {
			return false
		}

		p.literal++

		return true
	case *filter.IdentNode:
		p.ident++
		return n != nil
	case *filter.BinaryOpNode:
		if n == nil {
			return false
		}

		p.binary++

		return p.add(n.Left, seen) && p.add(n.Right, seen)
	case *filter.UnaryOpNode:
		if n == nil {
			return false
		}

		p.unary++

		return p.add(n.Operand, seen)
	case *filter.CallNode:
		if n == nil {
			return false
		}

		p.call++
		p.children += len(n.Args)

		return p.add(n.Target, seen) && p.addAll(n.Args, seen)
	case *filter.ListNode:
		if n == nil {
			return false
		}

		p.list++
		p.children += len(n.Elements)

		return p.addAll(n.Elements, seen)
	default:
		return false
	}
}

func (p *clonePlan) addAll(nodes []filter.Node, seen map[filter.Node]struct{}) bool {
	for _, n := range nodes {
		if !p.add(n, seen) {
			return false
		}
	}

	return true
}

// clone copies root, the tree the plan was made from.
func (p *clonePlan) clone(root filter.Node) filter.Node {
	s := slabs{
		binary:   make([]filter.BinaryOpNode, p.binary),
		unary:    make([]filter.UnaryOpNode, p.unary),
		call:     make([]filter.CallNode, p.call),
		list:     make([]filter.ListNode, p.list),
		ident:    make([]filter.IdentNode, p.ident),
		literal:  make([]filter.LiteralNode, p.literal),
		children: make([]filter.Node, p.children),
	}

	return s.copy(root)
}

// slabs holds the unused parts of a copy's backing arrays.
type slabs struct {
	binary   []filter.BinaryOpNode
	unary    []filter.UnaryOpNode
	call     []filter.CallNode
	list     []filter.ListNode
	ident    []filter.IdentNode
	literal  []filter.LiteralNode
	children []filter.Node
}

// take returns the next element of a slab and shrinks it.
func take[T any](slab *[]T) *T {
	v := &(*slab)[0]
	*slab = (*slab)[1:]

	return v
}

func (s *slabs) copy(n filter.Node) filter.Node {
	switch n := n.(type) {
	case *filter.LiteralNode:
		d := take(&s.literal)
		*d = *n

		if b, ok := n.Value.([]byte); ok {
			d.Value = slices.Clone(b)
		}

		return d
	case *filter.IdentNode:
		d := take(&s.ident)
		*d = *n

		return d
	case *filter.BinaryOpNode:
		d := take(&s.binary)
		*d = filter.BinaryOpNode{Op: n.Op, Left: s.copy(n.Left), Right: s.copy(n.Right)}

		return d
	case *filter.UnaryOpNode:
		d := take(&s.unary)
		*d = filter.UnaryOpNode{Op: n.Op, Operand: s.copy(n.Operand)}

		return d
	case *filter.CallNode:
		d := take(&s.call)
		*d = filter.CallNode{Op: n.Op, Target: s.copy(n.Target), Args: s.copyAll(n.Args)}

		return d
	case *filter.ListNode:
		d := take(&s.list)
		*d = filter.ListNode{Elements: s.copyAll(n.Elements)}

		return d
	default: // nil: the plan admits no other node
		return nil
	}
}

// copyAll copies nodes into the children slab, capping the result's capacity so appending to it cannot overwrite a
// sibling's children. A nil slice stays nil.
func (s *slabs) copyAll(nodes []filter.Node) []filter.Node {
	if nodes == nil {
		return nil
	}

	out := s.children[:len(nodes):len(nodes)]
	s.children = s.children[len(nodes):]

	for i, n := range nodes {
		out[i] = s.copy(n)
	}

	return out
}

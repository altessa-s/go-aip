// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"iter"
)

//go:generate go run golang.org/x/tools/cmd/stringer@v0.52.0 -type=NodeKind,Operator -output=node_string.go

// NodeKind represents the type of AST node.
type NodeKind uint8

const (
	// NodeKindLiteral represents literal values: "John", 18, true, nil.
	NodeKindLiteral NodeKind = iota
	// NodeKindIdent represents identifiers: name, address.city.
	NodeKindIdent
	// NodeKindBinaryOp represents binary operations: ==, !=, <, >, <=, >=, &&, ||, in.
	NodeKindBinaryOp
	// NodeKindUnaryOp represents unary operations: !.
	NodeKindUnaryOp
	// NodeKindCall represents function calls: contains(), startsWith(), size(), matches().
	NodeKindCall
	// NodeKindList represents list literals: [1, 2, 3].
	NodeKindList
)

// Operator represents operators used in expressions.
type Operator uint8

const (
	// OpEqual is the equality comparison operator (==).
	OpEqual Operator = iota
	// OpNotEqual is the inequality comparison operator (!=).
	OpNotEqual
	// OpLT is the less-than comparison operator (<).
	OpLT
	// OpLTE is the less-than-or-equal comparison operator (<=).
	OpLTE
	// OpGT is the greater-than comparison operator (>).
	OpGT
	// OpGTE is the greater-than-or-equal comparison operator (>=).
	OpGTE

	// OpAnd is the logical AND operator (&&).
	OpAnd
	// OpOr is the logical OR operator (||).
	OpOr
	// OpNot is the logical NOT operator (!).
	OpNot

	// OpIn is the membership test operator (in).
	OpIn

	// OpContains tests whether a string contains a substring.
	OpContains
	// OpStartsWith tests whether a string begins with a prefix.
	OpStartsWith
	// OpEndsWith tests whether a string ends with a suffix.
	OpEndsWith
	// OpMatches tests whether a string matches a regular expression.
	OpMatches

	// OpSize returns the length of a collection.
	OpSize
	// OpHas tests whether a field exists on the target.
	OpHas
	// OpExists is an alias for [OpHas].
	OpExists

	// OpSubstring returns a half-open [start, end) substring of a string.
	OpSubstring
)

// IsComparison returns true if the operator is a comparison operator.
func (o Operator) IsComparison() bool {
	return o >= OpEqual && o <= OpGTE
}

// IsLogical returns true if the operator is a logical operator.
func (o Operator) IsLogical() bool {
	return o >= OpAnd && o <= OpNot
}

// IsStringOp returns true if the operator is a boolean-returning string
// predicate: contains, startsWith, endsWith, matches. It does not
// include OpSubstring, which returns a string and is intended to be
// embedded inside comparisons rather than used as a predicate.
func (o Operator) IsStringOp() bool {
	return o >= OpContains && o <= OpMatches
}

// Node represents a node in the filter AST.
type Node interface {
	// Kind returns the type of this node.
	Kind() NodeKind
	// Accept allows a visitor to process this node.
	Accept(Visitor) (any, error)
	// Children returns an iterator over child nodes.
	Children() iter.Seq[Node]
}

// noChildren is the Children of a leaf node, shared so that asking a leaf for its children allocates nothing.
var noChildren iter.Seq[Node] = func(func(Node) bool) {}

// LiteralNode represents a literal value (string, number, bool, nil).
type LiteralNode struct {
	Value any
	// Text is the source spelling of a float literal ("1.00000000000000001") unless its float64 Value is certified to reproduce it
	// exactly as the shortest round-trip decimal (then empty); a spelling that cannot be recovered is a non-decimal marker.
	// [DecimalOperand] reads a [FieldKindDecimal] operand from it, so such an operand never falls back to the float.
	Text string
}

// Kind implements [Node].
func (n *LiteralNode) Kind() NodeKind { return NodeKindLiteral }

// Accept implements [Node].
func (n *LiteralNode) Accept(v Visitor) (any, error) { return v.VisitLiteral(n) }

// Children implements [Node].
func (n *LiteralNode) Children() iter.Seq[Node] { return noChildren }

// IdentNode represents an identifier or field reference.
type IdentNode struct {
	Name string // Field name, may include dots for nested fields (e.g., "address.city")
}

// Kind implements [Node].
func (n *IdentNode) Kind() NodeKind { return NodeKindIdent }

// Accept implements [Node].
func (n *IdentNode) Accept(v Visitor) (any, error) { return v.VisitIdent(n) }

// Children implements [Node].
func (n *IdentNode) Children() iter.Seq[Node] { return noChildren }

// BinaryOpNode represents a binary operation (comparison, logical, membership).
type BinaryOpNode struct {
	Op    Operator
	Left  Node
	Right Node
}

// Kind implements [Node].
func (n *BinaryOpNode) Kind() NodeKind { return NodeKindBinaryOp }

// Accept implements [Node].
func (n *BinaryOpNode) Accept(v Visitor) (any, error) { return v.VisitBinaryOp(n) }

// Children implements [Node].
func (n *BinaryOpNode) Children() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		if n.Left != nil && !yield(n.Left) {
			return
		}

		if n.Right != nil {
			yield(n.Right)
		}
	}
}

// UnaryOpNode represents a unary operation (negation).
type UnaryOpNode struct {
	Op      Operator
	Operand Node
}

// Kind implements [Node].
func (n *UnaryOpNode) Kind() NodeKind { return NodeKindUnaryOp }

// Accept implements [Node].
func (n *UnaryOpNode) Accept(v Visitor) (any, error) { return v.VisitUnaryOp(n) }

// Children implements [Node].
func (n *UnaryOpNode) Children() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		if n.Operand != nil {
			yield(n.Operand)
		}
	}
}

// CallNode represents a function/method call.
type CallNode struct {
	Op     Operator // The operation being performed (OpContains, OpStartsWith, etc.)
	Target Node     // The target of the call (for method calls like name.contains())
	Args   []Node   // Arguments to the function
}

// Kind implements [Node].
func (n *CallNode) Kind() NodeKind { return NodeKindCall }

// Accept implements [Node].
func (n *CallNode) Accept(v Visitor) (any, error) { return v.VisitCall(n) }

// Children implements [Node].
func (n *CallNode) Children() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		if n.Target != nil && !yield(n.Target) {
			return
		}

		for _, arg := range n.Args {
			if !yield(arg) {
				return
			}
		}
	}
}

// ListNode represents a list literal.
type ListNode struct {
	Elements []Node
}

// Kind implements [Node].
func (n *ListNode) Kind() NodeKind { return NodeKindList }

// Accept implements [Node].
func (n *ListNode) Accept(v Visitor) (any, error) { return v.VisitList(n) }

// Children implements [Node].
func (n *ListNode) Children() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		for _, elem := range n.Elements {
			if !yield(elem) {
				return
			}
		}
	}
}

// IsEquality reports whether the operator tests equality or inequality.
func (o Operator) IsEquality() bool { return o == OpEqual || o == OpNotEqual }

// ExtensionNode supplies a backend-neutral lowering for an application node or operator.
// Lower returns an equivalent built-in AST, without cycles. Parser snapshots lower extensions
// before caching, so validation, evaluation, translation and canonicalization all share its meaning.
// Unsupported backend operations in the expansion remain explicit errors.
// Implementations must be safe for concurrent calls and declare their dependencies through Children.
type ExtensionNode interface {
	Node
	Lower() (Node, error)
}

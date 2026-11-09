// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

// NegationNormalForm returns a deep copy of node with negations pushed to
// predicate leaves. arrayFields identifies fields whose membership predicates
// must retain an explicit OpNot. invertibleFields identifies fields whose null
// and comparison predicates may be rewritten as complements; other leaves
// retain an explicit OpNot to preserve their backend's null semantics.
//
// Scalar comparisons are complemented by inverting their operator. Negated
// string predicates remain wrapped in OpNot because their backend-specific
// complement may need a type guard. Negated has() becomes an explicit null
// comparison. A bare boolean identifier is normalized to equality with false.
func NegationNormalForm(node Node, arrayFields, invertibleFields map[string]bool) Node {
	return negationNormalForm(node, false, arrayFields, invertibleFields)
}

func negationNormalForm(node Node, negated bool, arrays, invertible map[string]bool) Node {
	switch n := node.(type) {
	case *UnaryOpNode:
		if n.Op == OpNot {
			return negationNormalForm(n.Operand, !negated, arrays, invertible)
		}

		return &UnaryOpNode{Op: n.Op, Operand: negationNormalForm(n.Operand, negated, arrays, invertible)}
	case *BinaryOpNode:
		return negateBinary(n, negated, arrays, invertible)
	case *IdentNode:
		if !negated {
			return &IdentNode{Name: n.Name}
		}

		if invertible[n.Name] {
			return &BinaryOpNode{Op: OpEqual, Left: &IdentNode{Name: n.Name}, Right: &LiteralNode{Value: false}}
		}

		return &UnaryOpNode{Op: OpNot, Operand: &IdentNode{Name: n.Name}}
	case *CallNode:
		return negateCall(n, negated, arrays, invertible)
	case *LiteralNode:
		literal := *n
		if negated {
			return &UnaryOpNode{Op: OpNot, Operand: &literal}
		}

		return &literal
	case *ListNode:
		clone := &ListNode{Elements: make([]Node, len(n.Elements))}
		for i := range n.Elements {
			clone.Elements[i] = negationNormalForm(n.Elements[i], false, arrays, invertible)
		}

		return clone
	default:
		if negated {
			return &UnaryOpNode{Op: OpNot, Operand: node}
		}

		return node
	}
}

// negateBinary is [negationNormalForm] of a binary operation: a negated AND or OR becomes the other junction of negated
// operands (De Morgan), a negated comparison that may be complemented takes the complementary operator, and any other
// negated operation keeps an explicit OpNot.
func negateBinary(n *BinaryOpNode, negated bool, arrays, invertible map[string]bool) Node {
	op := n.Op
	if negated {
		if keepsExplicitNot(n, arrays, invertible) {
			return &UnaryOpNode{Op: OpNot, Operand: negationNormalForm(n, false, arrays, invertible)}
		}

		op = complementOperator(op)
	}

	junction := negated && (op == OpAnd || op == OpOr)

	return &BinaryOpNode{
		Op: op, Left: negationNormalForm(n.Left, junction, arrays, invertible), Right: negationNormalForm(n.Right, junction, arrays, invertible),
	}
}

// keepsExplicitNot reports whether the negation of a binary operation other than AND and OR must stay an explicit OpNot:
// its field is not invertible, it is a membership test, or it compares an array field.
func keepsExplicitNot(n *BinaryOpNode, arrays, invertible map[string]bool) bool {
	switch n.Op {
	case OpAnd, OpOr:
		return false
	case OpIn:
		return true
	default:
	}

	ident, ok := n.Left.(*IdentNode)
	if size, isSize := n.Left.(*CallNode); isSize && size.Op == OpSize {
		ident, ok = size.Target.(*IdentNode)
	}

	if !ok || !invertible[ident.Name] {
		return true
	}

	return arrays[ident.Name] && n.Op.IsComparison()
}

// complementOperator returns the operator of the negation of a junction or comparison: AND and OR swap, and a
// comparison takes its complement. Any other operator is returned unchanged.
func complementOperator(op Operator) Operator {
	switch op {
	case OpAnd:
		return OpOr
	case OpOr:
		return OpAnd
	case OpEqual:
		return OpNotEqual
	case OpNotEqual:
		return OpEqual
	case OpLT:
		return OpGTE
	case OpLTE:
		return OpGT
	case OpGT:
		return OpLTE
	case OpGTE:
		return OpLT
	default:
		return op
	}
}

// negateCall is [negationNormalForm] of a function call: its operands are normalized without the negation, and a
// negated has() or exists() on an invertible field becomes a comparison with null; any other negated call keeps an
// explicit OpNot.
func negateCall(n *CallNode, negated bool, arrays, invertible map[string]bool) Node {
	clone := &CallNode{Op: n.Op, Target: negationNormalForm(n.Target, false, arrays, invertible), Args: make([]Node, len(n.Args))}
	for i := range n.Args {
		clone.Args[i] = negationNormalForm(n.Args[i], false, arrays, invertible)
	}

	if !negated {
		return clone
	}

	ident, hasIdent := n.Target.(*IdentNode)
	if (n.Op == OpHas || n.Op == OpExists) && hasIdent && invertible[ident.Name] {
		return &BinaryOpNode{Op: OpEqual, Left: clone.Target, Right: &LiteralNode{Value: nil}}
	}

	return &UnaryOpNode{Op: OpNot, Operand: clone}
}

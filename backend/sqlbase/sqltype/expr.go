// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

// Expr is one typed node of a checked filter. The tree mirrors the filter AST: a unary node has its operand as the only
// child, a binary node its two operands, a list its elements, and a call its target followed by its argument (string
// functions) or nothing (size(), has(), substring(), whose bounds are in Span). F is the dialect's field descriptor.
type Expr[F any] struct {
	// Tag is the kind of the source node.
	Tag filter.NodeKind
	// Op is the operator of a unary, binary or call node.
	Op filter.Operator
	// Kind is the operand kind: the element kind of an array, [sqlbase.Boolean] for a predicate.
	Kind sqlbase.Kind
	// Array is set for an array field and a list.
	Array bool
	// Value is the validated value of a literal, as the dialect's [Dialect.Literal] returned it; nil for a null literal.
	Value any
	// Node is the source node: the literal of a literal node and the identifier of a field.
	Node filter.Node
	// Field is the declaration of a field.
	Field F
	// Children are the typed operands.
	Children []*Expr[F]
	// Dimensions are the lengths of a list and its nested lists, outermost first.
	Dimensions []Dimension
	// Matcher is the compiled pattern of a matches() call.
	Matcher filter.RegexMatcher
	// RE2 reports whether Matcher is the policy's RE2 compiler.
	RE2 bool
	// Span holds the bounds of a substring() call.
	Span Span
}

// Dimension is the length and lower bound of one array dimension.
type Dimension struct {
	Length     int
	LowerBound int
}

// Span holds the validated bounds of a substring() call: the zero-based start, the length in code points (-1 to the end)
// and the guard, the length a string needs for the substring to exist (see [sqlbase.SubstringBounds]).
type Span struct{ Start, Length, Guard int64 }

// IsNull reports whether e is the null literal.
func (e *Expr[F]) IsNull() bool { return e.Tag == filter.NodeKindLiteral && e.Value == nil }

// IsField reports whether e is a field.
func (e *Expr[F]) IsField() bool { return e.Tag == filter.NodeKindIdent }

// IsSubstring reports whether e is a substring() call.
func (e *Expr[F]) IsSubstring() bool {
	return e.Tag == filter.NodeKindCall && e.Op == filter.OpSubstring
}

// IsNumber reports whether k is one of the numeric kinds.
func IsNumber(k sqlbase.Kind) bool {
	return k == sqlbase.Integer || k == sqlbase.Float || k == sqlbase.Numeric
}

// Compatible reports whether operands of kinds a and b compare: equal kinds, numbers, or an unknown kind.
func Compatible(a, b sqlbase.Kind) bool {
	return a == b || a == sqlbase.Unknown || b == sqlbase.Unknown || IsNumber(a) && IsNumber(b)
}

// ComparisonKind returns the kind two compatible operands compare as: Float over Numeric over the known kind.
func ComparisonKind(a, b sqlbase.Kind) sqlbase.Kind {
	if a == sqlbase.Float || b == sqlbase.Float {
		return sqlbase.Float
	}

	if a == sqlbase.Numeric || b == sqlbase.Numeric {
		return sqlbase.Numeric
	}

	if a == sqlbase.Unknown {
		return b
	}

	return a
}

// literalFits reports whether a literal of kind actual may stand for an operand of kind want.
func literalFits(want, actual sqlbase.Kind) bool {
	switch want {
	case sqlbase.Integer:
		return actual == sqlbase.Integer
	case sqlbase.Float:
		return IsNumber(actual)
	case sqlbase.Text, sqlbase.Boolean, sqlbase.Bytes, sqlbase.Timestamp:
		return want == actual
	default:
		return true
	}
}

// SetListKind propagates a common array kind through e and every nested list. Scalar operands keep their source kinds, and
// a node that is not a list is left unchanged.
func SetListKind[F any](e *Expr[F], kind sqlbase.Kind) {
	if e.Tag != filter.NodeKindList {
		return
	}

	e.Kind = kind
	for _, child := range e.Children {
		SetListKind(child, kind)
	}
}

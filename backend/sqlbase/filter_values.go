// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"time"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// operandSQL renders one side of a comparison: a column reference, a
// size expression over one, or a literal.
func (t *FilterTranslator) operandSQL(node filter.Node) (string, error) {
	switch n := node.(type) {
	case *filter.IdentNode:
		return t.column(n)
	case *filter.LiteralNode:
		value, err := t.visitLiteral(n)
		if err != nil {
			return "", err
		}

		return t.value(value)
	case *filter.CallNode:
		switch n.Op {
		case filter.OpSize:
			return t.sizeSQL(n)
		case filter.OpSubstring:
			sql, _, err := t.substringSQL(n)
			return sql, err
		default:
		}
	}

	return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%T as a comparison operand", node)
}

// sizeSQL renders a size() call through the dialect. The parser always puts
// the operand in Target; a hand-built node carrying it as the single
// argument is accepted too.
func (t *FilterTranslator) sizeSQL(n *filter.CallNode) (string, error) {
	target := n.Target
	if target == nil && len(n.Args) == 1 {
		target = n.Args[0]
	}

	col, err := t.column(target)
	if err != nil {
		return "", err
	}

	if t.isArrayField(target) {
		arrays, err := t.arrayDialect()
		if err != nil {
			return "", err
		}

		return arrays.ArraySize(col), nil
	}

	return t.dialect.SizeExpr(col), nil
}

// column resolves a node that must be a field reference into its quoted
// column name.
func (t *FilterTranslator) column(node filter.Node) (string, error) {
	ident, ok := node.(*filter.IdentNode)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field reference, got %T", node)
	}

	result, err := t.visitIdent(ident)
	if err != nil {
		return "", err
	}

	col, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "dialect returned %T for a column name", result)
	}

	return col, nil
}

// acceptPredicate visits a node expected to produce a boolean SQL
// clause. A bare identifier is treated as a boolean column test
// (`col = TRUE`), matching the CEL semantics of using a field directly
// as a condition; under negation it becomes `NOT (col = TRUE)`, which stays
// unknown for NULL as the evaluator finds a NULL condition unknown.
// An application predicate ([WithFilterPredicateRenderer]) is negated with NOT.
func (t *FilterTranslator) acceptPredicate(node filter.Node) (string, error) {
	if t.render != nil {
		if out, handled, err := t.render(node, t.value); handled || err != nil {
			if err == nil && t.negated {
				out = "NOT (" + out + ")"
			}

			return out, err
		}
	}

	if ident, ok := node.(*filter.IdentNode); ok {
		if t.isArrayField(ident) {
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "array field %s as a condition", ident.Name)
		}

		col, err := t.column(ident)
		if err != nil {
			return "", err
		}
		// The operand is a constant, never caller data, so it is
		// rendered inline in both modes rather than burning a bind slot.
		lit, err := t.dialect.FormatLiteral(true)
		if err != nil {
			return "", err
		}

		if t.negated {
			return "NOT (" + col + " = " + lit + ")", nil
		}

		return col + " = " + lit, nil
	}

	// A literal is a value, never a predicate, and it has to be turned away
	// before the walk rather than after it. visitLiteral returns the value
	// itself, so a string literal comes back as a Go string — which is
	// indistinguishable, by the type assertion below, from a rendered clause.
	// The expression `"1=1 OR ..."` would therefore have been emitted into the
	// WHERE clause verbatim, unquoted and unbound, defeating both the
	// allow-list and the placeholders.
	if lit, ok := node.(*filter.LiteralNode); ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got literal %T", lit.Value)
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	clause, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got %T", result)
	}

	return clause, nil
}

// value renders a literal for the active mode: a bind placeholder with
// the value pushed onto the argument slice, or inline SQL text. A null
// literal renders as NULL in both modes — there is nothing to bind.
func (t *FilterTranslator) value(v any) (string, error) {
	if v == nil {
		return "NULL", nil
	}

	if t.encode != nil {
		var err error

		v, err = t.encode(v)
		if err != nil {
			return "", err
		}
	}

	if t.inline {
		return t.dialect.FormatLiteral(v)
	}

	if t.encode == nil && !supportedValue(v) {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}

	if ts, ok := v.(time.Time); ok {
		if td, ok := t.dialect.(TimestampDialect); ok {
			return td.TimestampValue(ts, t.bind)
		}
	}

	return t.bind(v)
}

// bind collects v as the next query argument and returns its placeholder.
func (t *FilterTranslator) bind(v any) (string, error) {
	t.args = append(t.args, v)

	return t.dialect.Placeholder(len(t.args)), nil
}

// validateRegex applies the configured compiler, validator, and length limit.
// The dialect owns backend syntax support.
func (t *FilterTranslator) validateRegex(pattern string) error {
	_, err := t.config.CompileRegex(pattern)
	return err
}

// supportedValue reports whether a Go value can be bound as a query
// argument. The set is the intersection of what the CEL parser produces
// and what the SQL drivers accept.
func supportedValue(v any) bool {
	switch v.(type) {
	case bool, int64, uint64, float64, string, []byte, time.Time:
		return true
	default:
		return false
	}
}

// fieldOperandSQL preserves exact decimal spelling before a dialect encodes it.
func (t *FilterTranslator) fieldOperandSQL(node, field filter.Node) (string, error) {
	ident, ok := field.(*filter.IdentNode)
	if ok && t.config.FieldKind(ident.Name) == filter.FieldKindDecimal {
		if lit, ok := node.(*filter.LiteralNode); ok && lit.Value != nil {
			value, err := t.config.DecimalOperand(lit, lit.Value)
			if err != nil {
				return "", err
			}

			return t.renderValue(value)
		}
	}

	return t.operandSQL(node)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/internal/literalvalue"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Validate checks the backend-neutral shape and field rules of node using
// policy. It checks every branch, including branches an evaluator might skip.
// Backend-specific limits and literal constraints remain the responsibility of
// the backend. WithPortableContract enables additional rules shared by listing
// backends.
func Validate(node Node, policy *Policy) error {
	if policy == nil {
		return coreerrs.Wrap(ErrInvalidOptions, "nil filter policy")
	}

	if err := policy.CheckLimits(node); err != nil {
		return err
	}

	if node == nil {
		return nil
	}

	return (&validator{policy: policy}).predicate(node)
}

type validator struct {
	policy *Policy
}

func (v *validator) predicate(node Node) error {
	switch n := node.(type) {
	case *IdentNode:
		if _, err := v.field(n); err != nil {
			return err
		}

		if v.policy.ContractRuleEnabled(ScalarPredicates) && (v.policy.FieldKind(n.Name) != FieldKindBool || v.policy.IsArrayField(n.Name)) {
			return coreerrs.Wrapf(ErrFieldTypeMismatch, "field %q is not a scalar bool predicate", n.Name)
		}

		return nil
	case *BinaryOpNode:
		switch n.Op {
		case OpAnd, OpOr:
			if err := v.predicate(n.Left); err != nil {
				return err
			}

			return v.predicate(n.Right)
		case OpIn:
			return v.in(n.Left, n.Right)
		default:
			return v.comparison(n.Op, n.Left, n.Right)
		}
	case *UnaryOpNode:
		if n.Op != OpNot {
			return coreerrs.Wrapf(ErrUnsupportedOperation, "unary operator %v", n.Op)
		}

		return v.predicate(n.Operand)
	case *CallNode:
		return v.call(n)
	default:
		if nilcheck.IsNil(node) {
			return coreerrs.Wrap(ErrInvalidExpression, "nil AST node")
		}

		return coreerrs.Wrapf(ErrInvalidExpression, "expected a predicate, got %T", node)
	}
}

func (v *validator) field(node Node) (*IdentNode, error) {
	if err := CheckFieldReference(node); err != nil {
		return nil, err
	}

	switch n := node.(type) {
	case *IdentNode:
		if v.policy.ContractRuleEnabled(DeclaredFields) && v.policy.FieldKind(n.Name) == FieldKindUnspecified {
			return nil, ErrFieldTypeMismatch
		}

		if !v.policy.IsFieldAllowed(n.Name) {
			return nil, coreerrs.Wrapf(ErrFieldNotAllowed, "%s", n.Name)
		}

		return n, nil
	case *CallNode:
		return nil, coreerrs.Wrapf(ErrUnsupportedOperation, "function %v in field position", n.Op)
	default:
		return nil, coreerrs.Wrapf(ErrInvalidExpression, "expected field reference, got %T", node)
	}
}

func (v *validator) literal(node Node) (any, error) {
	lit, ok := node.(*LiteralNode)
	if !ok || lit == nil {
		return nil, coreerrs.Wrapf(ErrUnsupportedOperation, "expected literal operand, got %T", node)
	}

	switch lit.Value.(type) {
	case nil, bool, int64, uint64, float64, string, []byte, time.Time:
		return lit.Value, nil
	default:
		if literalvalue.Immutable(lit.Value) {
			return lit.Value, nil
		}

		return nil, coreerrs.Wrapf(ErrUnsupportedType, "%T", lit.Value)
	}
}

func (v *validator) operand(field *IdentNode, node Node) error {
	if v.policy.ContractRuleEnabled(FiniteFloats) && v.policy.FieldKind(field.Name) == FieldKindFloat {
		if lit, ok := node.(*LiteralNode); ok {
			if number, ok := lit.Value.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
				return coreerrs.Wrap(ErrInvalidExpression, "non-finite float literals are not portable")
			}
		}
	}

	if v.policy.FieldKind(field.Name) == FieldKindDecimal || v.policy.valueCodec(v.policy.FieldKind(field.Name)) != nil {
		return v.policy.CheckLiteralKind(field.Name, node)
	}

	_, err := v.literal(node)

	return err
}

func (v *validator) comparison(op Operator, left, right Node) error {
	if !op.IsComparison() {
		return coreerrs.Wrapf(ErrUnsupportedOperation, "comparison operator %v", op)
	}

	if call, ok := left.(*CallNode); ok && call.Op == OpSize {
		if call.Target == nil {
			return coreerrs.Wrap(ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
		}

		if _, err := v.field(call.Target); err != nil {
			return err
		}

		value, err := v.literal(right)
		if err == nil && v.policy.ContractRuleEnabled(OrderedNull) && isOrderedComparison(op) && value == nil {
			return coreerrs.Wrap(ErrInvalidExpression, "null is not allowed as an ordered comparison operand")
		}

		return err
	}

	if err := v.policy.CheckComparison(left, right); err != nil {
		return err
	}

	field, err := v.field(left)
	if err != nil {
		return err
	}

	if _, ok := right.(*IdentNode); ok {
		if v.policy.ContractRuleEnabled(LiteralComparisons) {
			return ErrUnsupportedOperation
		}

		_, err := v.field(right)

		return err
	}

	lit, ok := right.(*LiteralNode)
	if !ok || lit == nil {
		return ErrUnsupportedOperation
	}

	value := lit.Value
	if v.policy.ContractRuleEnabled(OrderedNull) && isOrderedComparison(op) && value == nil {
		return coreerrs.Wrap(ErrInvalidExpression, "null is not allowed as an ordered comparison operand")
	}

	return v.operand(field, right)
}

func (v *validator) in(left, right Node) error {
	if field, ok := right.(*IdentNode); ok && v.policy.IsArrayField(field.Name) && !IsFieldReference(left) {
		return v.comparison(OpEqual, right, left)
	}

	field, err := v.field(left)
	if err != nil {
		return err
	}

	if err = v.policy.CheckLiteralKind(field.Name, right); err != nil {
		return err
	}

	list, ok := right.(*ListNode)
	if !ok {
		return coreerrs.Wrapf(ErrInvalidExpression, "in operator requires a list, got %T", right)
	}

	if v.policy.ContractRuleEnabled(NonemptyIn) && len(list.Elements) == 0 {
		return coreerrs.Wrap(ErrInvalidExpression, "in operator requires a non-empty list")
	}

	for _, element := range list.Elements {
		lit, ok := element.(*LiteralNode)
		if !ok || lit == nil {
			return ErrUnsupportedOperation
		}

		value := lit.Value
		if v.policy.ContractRuleEnabled(NonnullIn) && value == nil {
			return coreerrs.Wrap(ErrInvalidExpression, "null is not allowed in an in list")
		}

		if err := v.operand(field, element); err != nil {
			return err
		}
	}

	return nil
}

func (v *validator) call(node *CallNode) error {
	switch node.Op {
	case OpContains, OpStartsWith, OpEndsWith, OpMatches:
		if _, err := v.field(node.Target); err != nil {
			return err
		}

		if len(node.Args) != 1 {
			return coreerrs.Wrap(ErrInvalidExpression, "string function requires exactly 1 argument")
		}

		arg, err := v.literal(node.Args[0])
		if err != nil {
			return err
		}

		pattern, ok := arg.(string)
		if !ok {
			return coreerrs.Wrap(ErrInvalidExpression, "string function argument must be a string")
		}

		if node.Op == OpMatches {
			if _, err := v.policy.CompileRegex(pattern); err != nil {
				return err
			}

			if v.policy.ContractRuleEnabled(PortableRegex) {
				if err := CheckPortableRegex(pattern); err != nil {
					return err
				}
			}
		}

		return nil
	case OpHas, OpExists:
		field, err := v.field(node.Target)
		if err != nil {
			return err
		}

		if v.policy.ContractRuleEnabled(ArrayPresence) && !v.policy.IsArrayField(field.Name) {
			return ErrUnsupportedOperation
		}

		return nil
	case OpSize:
		if node.Target == nil {
			return coreerrs.Wrap(ErrInvalidExpression, "size() must be called as a method, e.g. field.size()")
		}

		return coreerrs.Wrap(ErrUnsupportedOperation, "size() outside a comparison")
	default:
		return coreerrs.Wrapf(ErrUnsupportedOperation, "function %v", node.Op)
	}
}

func isOrderedComparison(op Operator) bool {
	return op == OpLT || op == OpLTE || op == OpGT || op == OpGTE
}

// CheckPortableRegex applies the [PortableRegex] contract rule to pattern: every backend reads it alike. The portable subset is
// regexp.QuoteMeta text plus the anchors ^ and $ and the wildcard .*, with an optional leading (?i), as functions.TextSearch
// emits, for an ASCII-only pattern (RE2, PCRE, ICU and PostgreSQL fold ASCII letters alike, but not others). It has no group,
// alternation, class or other quantifier, so it cannot backtrack exponentially in any engine. Any other pattern fails with
// [ErrUnsupportedOperation]. Every translator that enforces the rule, the SQL schema translators included, calls it.
func CheckPortableRegex(pattern string) error {
	if !portableRegex(pattern) {
		return coreerrs.Wrap(ErrUnsupportedOperation, "matches() pattern is outside the portable regex subset")
	}

	return nil
}

// portableRegex reports whether pattern is in the portable subset of [CheckPortableRegex].
func portableRegex(pattern string) bool {
	if rest, ok := strings.CutPrefix(pattern, "(?i)"); ok {
		if !isASCII(rest) {
			return false
		}

		pattern = rest
	}

	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '^', '$':
		case '.':
			if i+1 >= len(pattern) || pattern[i+1] != '*' {
				return false
			}

			i++
		case '*':
			return false
		case '\\':
			if i+1 >= len(pattern) || !isRegexMeta(pattern[i+1]) {
				return false
			}

			i++
		case '+', '?', '(', ')', '|', '[', ']', '{', '}':
			return false
		}
	}

	return true
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}

	return true
}

func isRegexMeta(char byte) bool {
	switch char {
	case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
		return true
	default:
		return false
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

// NormalizeDecimalOperand preserves source precision before a field's operand reaches a
// literal encoder. Non-decimal fields and non-literal nodes pass through unchanged.
// The returned literals and lists do not mutate the caller's AST.
func (o *Policy) NormalizeDecimalOperand(field, operand Node) (Node, error) {
	id, ok := field.(*IdentNode)
	if !ok || o.FieldKind(id.Name) != FieldKindDecimal {
		return operand, nil
	}

	switch n := operand.(type) {
	case *LiteralNode:
		if n.Value == nil {
			return operand, nil
		}

		value, err := o.DecimalOperand(n, n.Value)
		if err != nil {
			return nil, err
		}

		cloned := *n
		cloned.Value = value
		cloned.Text = ""

		return &cloned, nil
	case *ListNode:
		cloned := *n

		cloned.Elements = make([]Node, len(n.Elements))
		for i, element := range n.Elements {
			value, err := o.NormalizeDecimalOperand(field, element)
			if err != nil {
				return nil, err
			}

			cloned.Elements[i] = value
		}

		return &cloned, nil
	default:
		return operand, nil
	}
}

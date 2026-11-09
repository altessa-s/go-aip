// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"github.com/altessa-s/go-aip/fieldpolicy"

	filter "github.com/altessa-s/go-aip/filter"
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// enumVocabulary maps enum names to immutable stored values.
type enumVocabulary = coremaps.ImmutableMap[string, any]

// compileEnumNames validates and freezes enum names. Stored values must not contain mutable references;
// integers fitting int64 are normalized to the type produced by the parser.
func compileEnumNames(declared map[string]map[string]any) (*coremaps.ImmutableMap[string, *enumVocabulary], error) {
	fields := make(map[string]*enumVocabulary, len(declared))
	for field, names := range declared {
		if len(names) == 0 {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "enum field %q: no value names", field)
		}

		values := make(map[string]any, len(names))

		for name, v := range names {
			stored, _, ok := fieldpolicy.NormalizeEnumValue(v)
			if !ok {
				return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "enum field %q: value %q stores %T, want an immutable value", field, name, v)
			}

			values[name] = stored
		}

		fields[field] = coremaps.NewImmutableMap(values)
	}

	return coremaps.NewImmutableMap(fields), nil
}

// resolveEnumNames rewrites, in a freshly converted AST, every value name an enum field is compared with by its stored value. An enum
// field is compared only with ==, != and in, and is the target of no function but has() and size() (which measures an enum array or
// a stored string without comparing values); its literal operands are value names or null. A violation fails with [filter.ErrUnsupportedOperation],
// [filter.ErrFieldTypeMismatch] or, for an unknown name, [filter.ErrEnumValueNotAllowed].
// Messages name the field but never echo the literal: a filter may carry personal data.
func (p *Parser) resolveEnumNames(root filter.Node) error {
	var err error

	filter.Walk(root, func(n filter.Node) bool {
		switch n := n.(type) {
		case *filter.BinaryOpNode:
			err = p.resolveEnumComparison(n)
		case *filter.CallNode:
			if field, vocabulary := p.enumField(n.Target); vocabulary != nil && n.Op != filter.OpHas && n.Op != filter.OpExists && n.Op != filter.OpSize {
				err = coreerrs.Wrapf(filter.ErrUnsupportedOperation, "field %q is an enum: %v is not supported on it", field, n.Op)
			}
		}

		return err == nil
	})

	return err
}

// resolveEnumComparison rewrites the operands of one comparison or membership test that involves an enum field.
func (p *Parser) resolveEnumComparison(n *filter.BinaryOpNode) error {
	if !n.Op.IsComparison() && n.Op != filter.OpIn {
		return nil
	}

	field, vocabulary := p.enumField(n.Left)

	operand := n.Right
	if vocabulary == nil {
		// The mirrored spelling ("NAME" == state) and membership in an enum list ("NAME" in states).
		if field, vocabulary = p.enumField(n.Right); vocabulary == nil {
			return nil
		}

		operand = n.Left
	}

	if !p.orderedEnums && n.Op != filter.OpEqual && n.Op != filter.OpNotEqual && n.Op != filter.OpIn {
		return coreerrs.Wrapf(filter.ErrUnsupportedOperation, "field %q is an enum: only ==, != and in compare it", field)
	}

	if list, ok := operand.(*filter.ListNode); ok && n.Op == filter.OpIn && operand == n.Right {
		for _, el := range list.Elements {
			if err := resolveEnumLiteral(field, vocabulary, el); err != nil {
				return err
			}
		}

		return nil
	}

	return resolveEnumLiteral(field, vocabulary, operand)
}

// enumField returns the field and vocabulary of n when n names an enum field.
func (p *Parser) enumField(n filter.Node) (string, *enumVocabulary) {
	ident, ok := n.(*filter.IdentNode)
	if !ok || p.enumNames == nil {
		return "", nil
	}

	vocabulary, _ := p.enumNames.Get(ident.Name)

	return ident.Name, vocabulary
}

// resolveEnumLiteral replaces a value-name literal by its stored value; null and non-literal operands are left alone.
func resolveEnumLiteral(field string, vocabulary *enumVocabulary, n filter.Node) error {
	lit, ok := n.(*filter.LiteralNode)
	if !ok || lit.Value == nil {
		return nil
	}

	name, ok := lit.Value.(string)
	if !ok {
		return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "field %q is an enum: compare it with its value names", field)
	}

	stored, known := vocabulary.Get(name)
	if !known {
		return coreerrs.Wrapf(filter.ErrEnumValueNotAllowed, "field %q: not a known value name", field)
	}

	lit.Value, lit.Text = stored, ""

	return nil
}

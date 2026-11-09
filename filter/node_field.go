// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// IsFieldReference reports whether n can name a field: an identifier, or a
// size() call, whose target a translator resolves the same way. Translators
// resolve a field operand only after this check, so a string literal on the
// field side of a comparison — `"$where" == "x"` — cannot bypass the
// allow-list and field mapping applied to identifiers.
func IsFieldReference(n Node) bool {
	switch n := n.(type) {
	case *IdentNode:
		return true
	case *CallNode:
		return n.Op == OpSize
	default:
		return false
	}
}

// CheckFieldReference returns nil when n is a field reference (see
// [IsFieldReference]) and otherwise the error a translator reports for n in
// field position: [ErrUnsupportedOperation] for a function call the backend
// cannot use there (substring(), …), [ErrInvalidExpression] for anything else
// (a literal, an operator, a missing node).
//
// An identifier — directly or as the target of size() — must also be a dotted
// path of plain identifiers, each segment matching [A-Za-z_][A-Za-z0-9_]*, the
// grammar the orderby and projection paths enforce: anything else is
// [ErrInvalidExpression]. CEL's backtick escapes (a.`b c`), a leading dot (.a)
// and hand-built names with an empty segment ("a..b") therefore never reach a
// translator, which writes some field names into its output unquoted, or a
// wildcard allow-list entry ("a.*"), which would otherwise admit them.
func CheckFieldReference(n Node) error {
	switch n := n.(type) {
	case nil:
		return coreerrs.Wrap(ErrInvalidExpression, "missing field reference")
	case *IdentNode:
		return checkFieldPath(n)
	case *CallNode:
		if n == nil {
			break
		}

		if n.Op != OpSize {
			return coreerrs.Wrapf(ErrUnsupportedOperation, "function %v in field position", n.Op)
		}

		if ident, ok := n.Target.(*IdentNode); ok {
			return checkFieldPath(ident)
		}

		return nil
	}

	return coreerrs.Wrapf(ErrInvalidExpression, "expected field reference, got %T", n)
}

// checkFieldPath returns [ErrInvalidExpression] unless ident names a dotted path of plain identifiers.
func checkFieldPath(ident *IdentNode) error {
	if ident == nil {
		return coreerrs.Wrap(ErrInvalidExpression, "missing field reference")
	}

	if !IsFieldPath(ident.Name) {
		return coreerrs.Wrapf(ErrInvalidExpression, "invalid field path %q", ident.Name)
	}

	return nil
}

// IsFieldPath reports whether name is a dotted path of plain identifiers: one or more segments separated by single dots,
// each matching [A-Za-z_][A-Za-z0-9_]*. It is the field grammar [CheckFieldReference] holds every field reference to, so a
// translator or an [fieldpolicy.Access] implementation may rely on it for a name taken from a validated filter.
func IsFieldPath(name string) bool {
	start := true

	for i := range len(name) {
		c := name[i]
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			start = false
		case c >= '0' && c <= '9':
			if start {
				return false
			}
		case c == '.':
			if start {
				return false
			}

			start = true
		default:
			return false
		}
	}

	return !start
}

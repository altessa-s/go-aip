// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"fmt"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// translateContains handles field.contains("sub") → @field:*sub* on a TEXT
// field, @field:{*sub*} on a TAG field.
func (t *FilterTranslator) translateContains(target filter.Node, args []filter.Node) (string, error) {
	return t.translateTextSearch(target, args, "contains", "%s:*%s*", "%s:{*%s*}")
}

// translateStartsWith handles field.startsWith("pre") → @field:pre* on a TEXT
// field, @field:{pre*} on a TAG field.
func (t *FilterTranslator) translateStartsWith(target filter.Node, args []filter.Node) (string, error) {
	return t.translateTextSearch(target, args, "startsWith", "%s:%s*", "%s:{%s*}")
}

// translateEndsWith handles field.endsWith("suf") → @field:*suf on a TEXT
// field, @field:{*suf} on a TAG field (suffix queries, RediSearch 2.6+).
func (t *FilterTranslator) translateEndsWith(target filter.Node, args []filter.Node) (string, error) {
	return t.translateTextSearch(target, args, "endsWith", "%s:*%s", "%s:{*%s}")
}

// translateTextSearch renders a single-argument string search. The two
// callers differ only in the wildcards they wrap the needle in, which
// textFormat and tagFormat carry as templates over (field, needle); name
// appears in the error messages.
//
// The template follows the field's schema type: a TAG field needs the
// wildcard inside the tag braces — the TEXT form against a TAG field matches
// nothing at all. RediSearch matches both case-insensitively (a TAG field
// unless declared CASESENSITIVE); infix and suffix queries need RediSearch
// 2.6+ and run efficiently only on a field declared WITHSUFFIXTRIE. A NUMERIC
// field has no string form to search, so the call is rejected rather than sent
// as a query the server refuses.
func (t *FilterTranslator) translateTextSearch(
	target filter.Node, args []filter.Node, name, textFormat, tagFormat string,
) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	if len(args) != 1 {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires exactly 1 argument", name)
	}

	arg, err := filter.AcceptLiteral(visitor{t}, args[0])
	if err != nil {
		return "", err
	}

	s, ok := arg.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "%s argument must be a string", name)
	}

	format := textFormat

	switch t.fieldType(field) {
	case FieldTypeTag:
		format = tagFormat
	case FieldTypeNumeric:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "%s on NUMERIC field %q", name, field)
	case FieldTypeText:
	}

	// Every string contains, starts and ends with the empty string, so the call is the presence test; an empty wildcard such as
	// `@f:{**}` is no valid RediSearch term.
	if s == "" {
		return t.translateExists(target)
	}

	return fmt.Sprintf(format, queryField(field), t.escapeQueryValue(s)), nil
}

// translateExists handles has() / exists(): `-ismissing(@field)`, the
// documents that carry the attribute. ismissing needs the field declared
// INDEXMISSING (RediSearch 2.10+, DIALECT 2+), so only fields listed with
// [WithSchemaIndexMissing] are translated.
func (t *FilterTranslator) translateExists(target filter.Node) (string, error) {
	missing, err := t.missingClause(target)
	if err != nil {
		return "", err
	}

	return "-" + missing, nil
}

// missingClause renders `ismissing(@field)` for a field reference, or fails
// with [filter.ErrUnsupportedOperation] when the field is not declared
// INDEXMISSING ([WithSchemaIndexMissing]): without that index RediSearch cannot tell
// an absent attribute apart, and no other query form can either.
func (t *FilterTranslator) missingClause(target filter.Node) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	if _, ok := t.indexMissing[field]; !ok {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation,
			"presence test on field %q needs it declared INDEXMISSING (WithSchemaIndexMissing)", field)
	}

	return "ismissing(" + queryField(field) + ")", nil
}

// getFieldName extracts the field name from a node.
func (t *FilterTranslator) getFieldName(node filter.Node) (string, error) {
	if node != nil {
		if err := filter.CheckFieldReference(node); err != nil {
			return "", err
		}
	}

	return t.acceptString(node, "field name")
}

// getQueryString accepts a node and ensures the result is a string.
func (t *FilterTranslator) getQueryString(node filter.Node) (string, error) {
	return t.acceptString(node, "query string")
}

// acceptString visits a node and asserts that it produced a string. The
// two callers differ only in what they were expecting, which is what
// want names in the error.
func (t *FilterTranslator) acceptString(node filter.Node, want string) (string, error) {
	// A call can reach here with no target at all — `contains()` parses into a
	// CallNode whose Target is nil — and accepting a nil Node panics. That
	// makes a filter expression a client controls able to crash the process, so
	// the absent node is reported as the malformed expression it is.
	if node == nil {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "missing %s", want)
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	s, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected %s, got %T", want, result)
	}

	return s, nil
}

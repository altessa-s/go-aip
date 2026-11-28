// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"math"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

// renderSize renders size(). The default evaluator's size() of an absent array is 0, so outside the listing contract (whose
// arrays are NOT NULL) a NULL array column has cardinality 0.
func (r *renderer) renderSize(e *expression) (string, error) {
	a, err := r.render(e.Children[0])
	if err != nil {
		return "", err
	}

	switch c := e.Children[0]; {
	case c.Array:
		if !r.contract && c.Tag == filter.NodeKindIdent {
			return "COALESCE(cardinality(" + a + "), 0)", nil
		}

		return "cardinality(" + a + ")", nil
	case c.Kind == sqlbase.JSON:
		return "jsonb_array_length(" + a + ")", nil
	case c.Kind == sqlbase.Bytes:
		if r.contract && c.Field.presence {
			return "COALESCE(octet_length(" + a + "), 0)", nil
		}

		return "octet_length(" + a + ")", nil
	case r.contract && c.Kind == sqlbase.Text:
		if c.Field.presence {
			return "COALESCE(char_length(" + a + "), 0)", nil
		}

		return "char_length(" + a + ")", nil
	default:
		return "length(" + a + ")", nil
	}
}

// renderSubstring renders substring() as substr(), which counts characters from 1. It is NULL where the program finds the
// substring unknown: for a NULL text and, behind a char_length() guard, for a text too short to hold it, where substr()
// would return a truncated value. PostgreSQL text holds fewer than 2^30 characters, so a guard beyond the int4 range of
// substr() is never met and the value is NULL.
func (r *renderer) renderSubstring(e *expression) (string, error) {
	a, err := r.renderValue(e.Children[0])
	if err != nil {
		return "", err
	}

	span := e.Span
	if span.Guard >= math.MaxInt32 {
		return "(NULL)::text", nil
	}

	sql := "substr(" + a + ", " + strconv.FormatInt(span.Start+1, 10)
	if span.Length >= 0 {
		sql += ", " + strconv.FormatInt(span.Length, 10)
	}

	sql += ")"
	if span.Guard == 0 {
		return sql, nil
	}

	return "(CASE WHEN char_length(" + a + ") >= " + strconv.FormatInt(span.Guard, 10) + " THEN " + sql + " END)", nil
}

// renderHas renders has() of the column a renders: the column holds a value or, for a NOT NULL column of the listing contract,
// a value other than its zero. The test is never unknown.
func (r *renderer) renderHas(target *expression, a string) (string, error) {
	if !r.contract || nullable(target, true) {
		return "((" + a + ") IS NOT NULL)", nil
	}

	if target.Array {
		return "(cardinality(" + a + ") > 0)", nil
	}

	if target.Kind == sqlbase.UUID {
		return sqlTrue, nil
	}

	zero, err := r.literal(zeroValue(target.Kind), target.Kind)
	if err != nil {
		return "", err
	}

	return "(" + a + " <> " + zero + ")", nil
}

// renderCallPredicate renders has() and the string and JSON functions. A NULL target contains nothing, so the negation of a
// function holds for it; a NULL argument is unknown.
func (r *renderer) renderCallPredicate(e *expression, negated bool) (string, error) {
	target := e.Children[0]

	a, err := r.render(target)
	if err != nil {
		return "", err
	}

	if e.Op == filter.OpHas || e.Op == filter.OpExists {
		sql, hasErr := r.renderHas(target, a)
		if hasErr != nil || !negated {
			return sql, hasErr
		}

		return "(NOT " + sql + ")", nil
	}

	// The argument is rendered last, so that contractPattern finds its bound value.
	c, err := r.render(e.Children[1])
	if err != nil {
		return "", err
	}

	var sql string

	switch {
	case target.Kind == sqlbase.JSON:
		sql = "(" + a + " @> " + c + ")"
	case !target.Array:
		if op, pattern, ok := contractPattern(r, e); ok {
			sql = "(" + a + " " + op + " " + pattern + ")"
		} else if sql, err = renderStringCall(e, a, c); err != nil {
			return "", err
		}
	default:
		return r.renderArrayStringCall(e, a, c, negated)
	}

	switch {
	case !negated:
		return sql, nil
	case nullable(target, r.contract):
		return "(NOT " + sql + " OR (" + a + ") IS NULL)", nil
	default:
		return "(NOT " + sql + ")", nil
	}
}

// renderArrayStringCall renders a string function over the elements of the array a renders, with c its argument: some element
// satisfies it. A NULL array has no element and a NULL element satisfies nothing; a NULL argument is unknown.
func (r *renderer) renderArrayStringCall(e *expression, a, c string, negated bool) (string, error) {
	alias := r.arrayAlias()

	element := alias
	if r.collated() {
		element = "(" + alias + " COLLATE " + r.collation() + ")"
	}

	pred, err := renderStringCall(e, element, c)
	if err != nil {
		return "", err
	}

	if op, pattern, ok := contractPattern(r, e); ok {
		pred = "(" + element + " " + op + " " + pattern + ")"
	}

	sql := "EXISTS (SELECT 1 FROM unnest(" + a + ") AS " + alias + " WHERE " + pred + ")"

	switch needle := e.Children[1]; {
	case !negated:
		return sql, nil
	case needle.Tag == filter.NodeKindLiteral && !needle.IsNull():
		return "(NOT " + sql + ")", nil
	default:
		return "((" + c + ") IS NOT NULL AND NOT " + sql + ")", nil
	}
}

// contractPattern renders a matches() pattern under the listing contract, which reads the pattern as RE2 and MongoDB do: the
// leading (?i) becomes ~*, and the ARE option (?p) keeps . and negated classes from matching a newline (PostgreSQL's default lets
// them) without making ^ and $ line anchors.
func contractPattern(r *renderer, e *expression) (op, rendered string, ok bool) {
	if !r.contract || e.Op != filter.OpMatches || !e.RE2 {
		return "", "", false
	}

	pattern, ok := e.Children[1].Value.(string)
	if !ok {
		return "", "", false
	}

	op = "~"
	if rest, insensitive := strings.CutPrefix(pattern, "(?i)"); insensitive {
		op, pattern = "~*", rest
	}

	pattern = "(?p)" + pattern
	if r.inline {
		return op, "(" + quoteString(pattern) + ")::text", true
	}

	if len(r.args) > 0 {
		r.args[len(r.args)-1] = pattern
	}

	return op, "($" + strconv.Itoa(len(r.args)) + ")::text", true
}

func renderStringCall(e *expression, a, c string) (string, error) {
	switch e.Op {
	case filter.OpContains:
		if e.Children[0].Field.foldContains {
			a = "lower((" + a + ") COLLATE pg_catalog.pg_c_utf8)"
			c = "lower((" + c + ") COLLATE pg_catalog.pg_c_utf8)"
		}

		return "(strpos(" + a + ", " + c + ") > 0)", nil
	case filter.OpStartsWith:
		return "starts_with(" + a + ", " + c + ")", nil
	case filter.OpEndsWith:
		return "(right(" + a + ", length(" + c + ")) = " + c + ")", nil
	case filter.OpMatches:
		return "(" + a + " ~ " + c + ")", nil
	default:
		return "", unsupported()
	}
}

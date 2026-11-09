// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"bytes"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// sqlTrue and sqlFalse are the constant predicates.
const (
	sqlTrue  = "TRUE"
	sqlFalse = "FALSE"
)

type renderer struct {
	options  schemaOptions
	inline   bool
	contract bool
	args     []any
	names    map[string]struct{}
	alias    int
}

// SQL renders a parameterized PostgreSQL WHERE clause and its bound arguments.
// Text comparisons explicitly use C collation, matching in-memory byte ordering.
func (p *Program) SQL() (string, []any, error) {
	r := &renderer{}
	return p.sqlWith(r)
}

// SQLContract renders against the non-null storage contract used by listing.
// Columns carry their own C collation and repeated values are non-null arrays.
func (p *Program) SQLContract() (string, []any, error) {
	return p.sqlWith(&renderer{contract: true})
}

func (p *Program) sqlWith(r *renderer) (string, []any, error) {
	r.options = p.options

	sql, err := r.renderRoot(p.root)
	if err != nil {
		return "", nil, err
	}

	return sql, r.args, nil
}

// Inline renders the same expression with safely quoted typed literals. The output is pure ASCII, so it reads the same in
// every client encoding: text literals escape their non-ASCII code points, and a column or collation name with a byte >=
// 0x80 (admitted only by a custom column validator) fails with filter.ErrInvalidExpression.
func (p *Program) Inline() (string, error) {
	return p.inline(false)
}

// InlineContract renders inline SQL for the listing storage contract, in pure ASCII like [Program.Inline].
func (p *Program) InlineContract() (string, error) {
	return p.inline(true)
}

func (p *Program) inline(contract bool) (string, error) {
	if err := asciiNames(p.root, p.options.textCollation); err != nil {
		return "", err
	}

	return (&renderer{inline: true, contract: contract, options: p.options}).renderRoot(p.root)
}

// asciiNames rejects a collation or a column of e with a byte >= 0x80: inline SQL must read the same in every client
// encoding, and PostgreSQL has no escape for an identifier that works whatever standard_conforming_strings is.
func asciiNames(e *expression, collation string) error {
	if !isASCII(collation) {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "inline SQL requires an ASCII collation name")
	}

	if e == nil {
		return nil
	}

	if e.Tag == filter.NodeKindIdent && !isASCII(e.Field.column) {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "inline SQL requires ASCII column names")
	}

	for _, child := range e.Children {
		if err := asciiNames(child, ""); err != nil {
			return err
		}
	}

	return nil
}

func (r *renderer) renderRoot(e *expression) (string, error) {
	if e == nil {
		return "1 = 1", nil
	}

	r.reserveNames(e)

	return r.predicate(e, false)
}

// Both relation and column names can be shadowed by a FROM alias. Reserve all
// identifier components before introducing any local names for array predicates.
func (r *renderer) reserveNames(e *expression) {
	if e.Tag == filter.NodeKindIdent {
		if r.names == nil {
			r.names = make(map[string]struct{})
		}

		for part := range strings.SplitSeq(e.Field.column, ".") {
			r.names[part] = struct{}{}
		}
	}

	for _, child := range e.Children {
		r.reserveNames(child)
	}
}

func (r *renderer) arrayAlias() string {
	if r.names == nil {
		r.names = make(map[string]struct{})
	}

	for {
		name := r.options.arrayAliasPrefix
		if name == "" {
			name = DefaultArrayAliasPrefix
		}

		name += strconv.Itoa(r.alias)
		r.alias++

		if _, exists := r.names[name]; !exists {
			r.names[name] = struct{}{}
			return `"` + name + `"`
		}
	}
}

func (r *renderer) literal(v any, k sqlbase.Kind) (string, error) {
	if v == nil {
		return "NULL", nil
	}

	if k == sqlbase.JSON {
		v = jsonText(v)
	}

	if n, ok := v.(sqltype.Number); ok {
		v = numberText(n)
	}

	if !r.inline {
		if blob, ok := v.([]byte); ok {
			v = bytes.Clone(blob)
		}

		r.args = append(r.args, v)

		return "($" + strconv.Itoa(len(r.args)) + ")::" + typeName(k), nil
	}

	text, err := inlineValue(v)
	if err != nil {
		return "", err
	}

	return "(" + text + ")::" + typeName(k), nil
}

func inlineValue(v any) (string, error) {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		switch {
		case math.IsNaN(x):
			return "'NaN'", nil
		case math.IsInf(x, 1):
			return "'Infinity'", nil
		case math.IsInf(x, -1):
			return "'-Infinity'", nil
		default:
			return strconv.FormatFloat(x, 'g', -1, 64), nil
		}
	case bool:
		return strconv.FormatBool(x), nil
	case string:
		return quoteString(x), nil
	case []byte:
		return "decode('" + hex.EncodeToString(x) + "', 'hex')", nil
	case time.Time:
		return quoteString(x.UTC().Format("2006-01-02 15:04:05.000000Z07:00")), nil
	default:
		return "", typeError()
	}
}

// render renders e as a value. Outside the listing contract a text value carries the C collation. A predicate used as a value
// (an operand of a comparison, a list element) renders its exact result: TRUE, FALSE, or NULL where the program's is unknown.
func (r *renderer) render(e *expression) (string, error) {
	sql, err := r.renderValue(e)
	if err != nil {
		return "", err
	}

	if e.Kind == sqlbase.Text && !r.contract {
		sql = "(" + sql + " COLLATE " + r.collation() + ")"
	}

	return sql, nil
}

// renderValue returns an atomic SQL expression: compound predicates retain
// their own parentheses even when nested inside another comparison or list.
func (r *renderer) renderValue(e *expression) (string, error) {
	switch e.Tag {
	case filter.NodeKindLiteral:
		return r.literal(e.Value, e.Kind)
	case filter.NodeKindIdent:
		return renderField(e)
	case filter.NodeKindList:
		return r.renderList(e)
	case filter.NodeKindCall:
		switch e.Op {
		case filter.OpSize:
			return r.renderSize(e)
		case filter.OpSubstring:
			return r.renderSubstring(e)
		default:
			return r.exact(e)
		}
	case filter.NodeKindUnaryOp, filter.NodeKindBinaryOp:
		return r.exact(e)
	default:
		return "", unsupported()
	}
}

// exact renders predicate e as a value that is TRUE where the program evaluates e to true, FALSE where to false and NULL where
// the result is unknown.
func (r *renderer) exact(e *expression) (string, error) {
	holds, err := r.predicate(e, false)
	if err != nil {
		return "", err
	}

	fails, err := r.predicate(e, true)
	if err != nil {
		return "", err
	}

	return "(CASE WHEN " + holds + " THEN TRUE WHEN " + fails + " THEN FALSE END)", nil
}

// predicate renders the boolean expression e as a WHERE clause that is TRUE exactly where the program evaluates e to true or,
// negated, to false. `!` is pushed down to the predicates (De Morgan over && and ||) instead of rendered as NOT around a clause:
// SQL's NOT keeps an unknown result unknown, while a predicate the default evaluator finds false for a NULL operand (`x == v`,
// `x in [...]`, a string function) must hold under negation. Each predicate renders its own exact negation.
func (r *renderer) predicate(e *expression, negated bool) (string, error) {
	switch e.Tag {
	case filter.NodeKindUnaryOp:
		return r.predicate(e.Children[0], !negated)
	case filter.NodeKindBinaryOp:
		switch e.Op {
		case filter.OpAnd, filter.OpOr:
			a, err := r.predicate(e.Children[0], negated)
			if err != nil {
				return "", err
			}

			c, err := r.predicate(e.Children[1], negated)
			if err != nil {
				return "", err
			}

			connective := ") AND ("
			if (e.Op == filter.OpOr) != negated {
				connective = ") OR ("
			}

			return "((" + a + connective + c + "))", nil
		case filter.OpIn:
			return r.renderIn(e, negated)
		default:
			return r.renderComparison(e, negated)
		}
	case filter.NodeKindCall:
		return r.renderCallPredicate(e, negated)
	default:
		// A boolean column or literal: NULL is unknown, as the default evaluator finds a null condition.
		sql, err := r.render(e)
		if err != nil || !negated {
			return sql, err
		}

		return "(NOT (" + sql + "))", nil
	}
}

func renderField(e *expression) (string, error) {
	col, err := quoteColumn(e.Field.column)
	if err != nil {
		return "", err
	}

	if e.Field.json {
		parts := make([]string, len(e.Field.path))
		for i, p := range e.Field.path {
			parts[i] = quoteString(p)
		}

		op := "#>"
		if e.Kind != sqlbase.JSON {
			op = "#>>"
		}

		col = "(" + col + " " + op + " ARRAY[" + strings.Join(parts, ", ") + "]::text[])"
		if e.Kind != sqlbase.JSON {
			col = "(" + col + ")::" + typeName(e.Kind)
		}
	}

	return col, nil
}

func (r *renderer) renderList(e *expression) (string, error) {
	parts := make([]string, len(e.Children))
	for i, c := range e.Children {
		v, err := r.render(c)
		if err != nil {
			return "", err
		}

		parts[i] = v
	}

	return "ARRAY[" + strings.Join(parts, ", ") + "]::" + typeName(e.Kind) + "[]", nil
}

func (r *renderer) collation() string {
	name := r.options.textCollation
	if name == "" {
		name = DefaultTextCollation
	}

	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

type contractField struct {
	column          string
	kind            sqlbase.Kind
	array, presence bool
}

// contractRenderer renders one checked filter from its typed tree. It owns the argument buffer of one Translate call.
type contractRenderer struct {
	policy  *filter.Policy
	options *schemaOptions
	// digits is the fractional-second digits of the timestamp operands.
	digits int
	args   []any
	names  map[string]bool
	alias  int
	// negated marks a predicate under an odd number of `!` operators: negation is pushed down to the predicates, see
	// [contractRenderer.predicate].
	negated bool
}

// reserve reserves the column names of every field in e, so no array alias takes one.
func (r *contractRenderer) reserve(e *contractExpression) {
	if e.IsField() {
		for part := range strings.SplitSeq(e.Field.Column(), ".") {
			r.names[part] = true
		}
	}

	for _, child := range e.Children {
		r.reserve(child)
	}
}

// field returns the storage of a field operand; any other operand has none (an unknown kind).
func field(e *contractExpression) contractField {
	if !e.IsField() {
		return contractField{}
	}

	return contractField{e.Field.Column(), e.Field.Kind(), e.Field.IsArray(), e.Field.HasPresence()}
}

func (r *contractRenderer) bind(v any) string {
	if b, ok := v.([]byte); ok {
		v = bytes.Clone(b)
	}

	r.args = append(r.args, v)

	return "?"
}

func (r *contractRenderer) operand(e *contractExpression, kind sqlbase.Kind, jsonValue bool) (any, error) {
	lit, ok := e.Node.(*filter.LiteralNode)
	if !ok {
		return nil, filter.ErrUnsupportedOperation
	}

	return r.literalValue(lit, kind, jsonValue)
}

// literalValue returns the bound value of lit as an operand of kind, in JSON form for a JSON document.
func (r *contractRenderer) literalValue(lit *filter.LiteralNode, kind sqlbase.Kind, jsonValue bool) (any, error) {
	v := lit.Value
	if v == nil {
		return nil, nil //nolint:nilnil // a null literal binds as SQL NULL, not an error
	}

	if kind == sqlbase.Numeric {
		d, err := r.policy.DecimalOperand(lit, v)
		if err != nil {
			return nil, err
		}

		if err := r.options.decimalFormat.Check(d); err != nil {
			return nil, err
		}

		if jsonValue {
			return json.Number(d.String()), nil
		}

		return d.String(), nil
	}

	if t, ok := v.(time.Time); ok {
		precision := time.Second
		for range r.digits {
			precision /= 10
		}

		t = t.UTC().Truncate(precision)
		if jsonValue {
			return t.Format(r.timestampLayout()), nil
		}

		return t, nil
	}

	return v, nil
}

func (r *contractRenderer) literal(e *contractExpression, kind sqlbase.Kind) (string, error) {
	lit, ok := e.Node.(*filter.LiteralNode)
	if !ok {
		return "", filter.ErrUnsupportedOperation
	}

	return r.bindLiteral(lit, kind)
}

// bindLiteral binds lit as an operand of kind, cast to the SQL type of a decimal or a timestamp.
func (r *contractRenderer) bindLiteral(lit *filter.LiteralNode, kind sqlbase.Kind) (string, error) {
	v, err := r.literalValue(lit, kind, false)
	if err != nil {
		return "", err
	}

	if v == nil {
		return "NULL", nil
	}

	mark := r.bind(v)
	if kind == sqlbase.Numeric {
		typ, err := r.options.decimalFormat.SQL()
		if err != nil {
			return "", err
		}

		return "CAST(" + mark + " AS " + typ + ")", nil
	}

	if kind == sqlbase.Timestamp {
		return "CAST(" + mark + " AS DATETIME(" + strconv.Itoa(r.digits) + "))", nil
	}

	return mark, nil
}

func (r *contractRenderer) jsonOperand(e *contractExpression, kind sqlbase.Kind) (string, error) {
	var v any

	if e.Tag == filter.NodeKindList {
		values := make([]any, len(e.Children))
		for i, child := range e.Children {
			value, err := r.operand(child, kind, true)
			if err != nil {
				return "", err
			}

			values[i] = value
		}

		v = values
	} else {
		value, err := r.operand(e, kind, true)
		if err != nil {
			return "", err
		}

		v = value
	}

	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return r.bind(string(encoded)), nil
}

// render renders n as a value. A predicate used as a value (an operand of a comparison) renders its exact result: TRUE,
// FALSE, or NULL where the default evaluator finds it unknown.
func (r *contractRenderer) render(e *contractExpression, hint sqlbase.Kind) (string, error) {
	switch e.Tag {
	case filter.NodeKindIdent:
		parts := strings.Split(e.Field.Column(), ".")
		for i, part := range parts {
			if part == "" || strings.ContainsRune(part, 0) {
				return "", filter.ErrInvalidExpression
			}

			if utf8.RuneCountInString(part) > sqlbase.MariaDBMaxIdentLen {
				return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "SQL identifier %q exceeds %d characters", part,
					sqlbase.MariaDBMaxIdentLen)
			}

			parts[i] = "`" + strings.ReplaceAll(part, "`", "``") + "`"
		}

		return strings.Join(parts, "."), nil
	case filter.NodeKindLiteral:
		return r.literal(e, hint)
	case filter.NodeKindCall:
		switch e.Op {
		case filter.OpSize:
			return r.size(e)
		case filter.OpSubstring:
			return r.substring(e)
		default:
			return r.exact(e)
		}
	case filter.NodeKindUnaryOp, filter.NodeKindBinaryOp:
		return r.exact(e)
	default:
		// A list is not a value of its own.
		return "", filter.ErrUnsupportedOperation
	}
}

// exact renders the predicate n as a value: TRUE where it holds, FALSE where its negation holds, NULL otherwise.
func (r *contractRenderer) exact(e *contractExpression) (string, error) {
	saved := r.negated
	defer func() { r.negated = saved }()

	r.negated = false

	holds, err := r.predicate(e)
	if err != nil {
		return "", err
	}

	r.negated = true

	fails, err := r.predicate(e)
	if err != nil {
		return "", err
	}

	return "(CASE WHEN " + holds + " THEN TRUE WHEN " + fails + " THEN FALSE END)", nil
}

// predicate renders the boolean expression n as a WHERE clause that is TRUE exactly where the default evaluator finds n true or,
// negated, false. `!` is pushed down to the predicates (De Morgan over && and ||) instead of rendered as NOT around a clause:
// SQL's NOT keeps an unknown (NULL) result unknown, while the evaluator finds `x == v`, `x in [...]` and a string function
// false for a null presence field, so their negation must match it. Each predicate renders its own exact negation.
func (r *contractRenderer) predicate(e *contractExpression) (string, error) {
	switch e.Tag {
	case filter.NodeKindUnaryOp:
		r.negated = !r.negated
		sql, err := r.predicate(e.Children[0])
		r.negated = !r.negated

		return sql, err
	case filter.NodeKindBinaryOp:
		return r.binary(e)
	case filter.NodeKindCall:
		return r.call(e)
	default:
		// A boolean column or literal: NULL is unknown, as the evaluator finds a null condition unknown.
		sql, err := r.render(e, sqlbase.Boolean)
		if err != nil || !r.negated {
			return sql, err
		}

		return "(NOT (" + sql + "))", nil
	}
}

func comparison(op filter.Operator) string {
	switch op {
	case filter.OpEqual:
		return "="
	case filter.OpNotEqual:
		return "<>"
	case filter.OpLT:
		return "<"
	case filter.OpLTE:
		return "<="
	case filter.OpGT:
		return ">"
	case filter.OpGTE:
		return ">="
	case filter.OpAnd:
		return "AND"
	case filter.OpOr:
		return "OR"
	default:
		// Not a comparison or a connective: no SQL operator.
	}

	return ""
}

func zero(kind sqlbase.Kind) any {
	switch kind {
	case sqlbase.Boolean:
		return false
	case sqlbase.Integer, sqlbase.Numeric:
		return int64(0)
	case sqlbase.Float:
		return float64(0)
	case sqlbase.Bytes:
		return []byte{}
	case sqlbase.Timestamp:
		return time.Time{}
	default:
		return ""
	}
}

// absence renders the null test of n: a presence column IS [NOT] NULL, the zero-value test of a NOT NULL column (an empty
// array). The test is never unknown.
func (r *contractRenderer) absence(e *contractExpression, absent bool) (string, error) {
	f := field(e)

	col, err := r.render(e, f.kind)
	if err != nil {
		return "", err
	}

	if f.presence {
		op := "IS NOT NULL"
		if absent {
			op = "IS NULL"
		}

		return "((" + col + ") " + op + ")", nil
	}

	op := "<>"
	if absent {
		op = "="
	}

	if f.array {
		return "(JSON_LENGTH(" + col + ") " + op + " 0)", nil
	}

	val, err := r.bindLiteral(&filter.LiteralNode{Value: zero(f.kind)}, f.kind)

	return "(" + col + " " + op + " " + val + ")", err
}

// nullComparison renders `n == null` (absent) or `n != null`. A column gets [contractRenderer.absence]; a literal is static;
// size() is never null, and a predicate used as a value is null only where the evaluator finds it unknown, so `== null` never
// holds for either.
func (r *contractRenderer) nullComparison(e *contractExpression, absent bool) (string, error) {
	switch e.Tag {
	case filter.NodeKindIdent:
		return r.absence(e, absent)
	case filter.NodeKindLiteral:
		if e.IsNull() == absent {
			return sqlTrue, nil
		}

		return sqlFalse, nil
	default:
		// size() or a predicate used as a value.
	}

	if absent {
		return sqlFalse, nil
	}

	sql, err := r.render(e, sqlbase.Unknown)

	return "((" + sql + ") IS NOT NULL)", err
}

// connective returns the SQL connective of op (&& or ||), swapped under negation.
func (r *contractRenderer) connective(op filter.Operator) string {
	if (op == filter.OpOr) != r.negated {
		return "OR"
	}

	return "AND"
}

// not wraps sql in NOT under negation, for a predicate that is never unknown.
func (r *contractRenderer) not(sql string) string {
	if r.negated {
		return "(NOT " + sql + ")"
	}

	return sql
}

//nolint:gocognit,gocyclo // one case per operand shape
func (r *contractRenderer) binary(e *contractExpression) (string, error) {
	left, right := e.Children[0], e.Children[1]

	if e.Op == filter.OpAnd || e.Op == filter.OpOr {
		leftSQL, err := r.predicate(left)
		if err != nil {
			return "", err
		}

		rightSQL, err := r.predicate(right)
		if err != nil {
			return "", err
		}

		return "(" + leftSQL + " " + r.connective(e.Op) + " " + rightSQL + ")", nil
	}

	op := e.Op
	if r.negated && op.IsEquality() {
		op = oppositeEquality(op)
	}

	if left.IsNull() {
		return r.nullComparison(right, op == filter.OpEqual)
	}

	if right.IsNull() {
		return r.nullComparison(left, op == filter.OpEqual)
	}

	lf, rf := field(left), field(right)

	if e.Op == filter.OpIn && rf.array {
		return r.membership(right, left, rf, r.negated)
	}

	if lf.array {
		switch e.Op {
		case filter.OpEqual, filter.OpNotEqual:
			return r.membership(left, right, lf, op == filter.OpNotEqual)
		case filter.OpIn:
			col, colErr := r.render(left, lf.kind)
			if colErr != nil {
				return "", colErr
			}

			arg, argErr := r.jsonOperand(right, lf.kind)

			return r.not("JSON_OVERLAPS(" + col + ", " + arg + ")"), argErr
		default:
			if right.Tag != filter.NodeKindLiteral {
				return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "an array is compared with a literal")
			}

			sql, predErr := r.arrayPredicate(left, lf, func(element string) (string, error) {
				val, valErr := r.render(right, lf.kind)
				return "(" + element + " " + comparison(e.Op) + " " + val + ")", valErr
			})

			return r.not(sql), predErr
		}
	}

	if e.Op == filter.OpIn {
		return r.in(left, right, lf)
	}

	return r.comparison(op, left, right, lf, rf)
}

// in renders `field in [...]` over literals; the checker leaves only a list on the right of a scalar `in`. A null presence
// column is in the list only with a null element, which IN never finds, so the null element becomes an IS NULL test; a NOT
// NULL column holds no null.
func (r *contractRenderer) in(left, list *contractExpression, lf contractField) (string, error) {
	col, err := r.render(left, lf.kind)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(list.Children))
	hasNull := false

	for _, child := range list.Children {
		if child.Tag != filter.NodeKindLiteral {
			return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "an in list holds literals")
		}

		if child.IsNull() {
			hasNull = true
			continue
		}

		part, err := r.render(child, lf.kind)
		if err != nil {
			return "", err
		}

		parts = append(parts, part)
	}

	nullable := left.IsField() && lf.presence
	matchesNull := nullable && hasNull

	if len(parts) == 0 {
		switch {
		case matchesNull && r.negated, r.negated && left.IsSubstring():
			// A substring() that does not exist is unknown, also under negation.
			return "((" + col + ") IS NOT NULL)", nil
		case matchesNull:
			return "((" + col + ") IS NULL)", nil
		case r.negated:
			return sqlTrue, nil
		default:
			return sqlFalse, nil
		}
	}

	switch {
	case !r.negated && matchesNull:
		return "(" + col + " IN (" + strings.Join(parts, ", ") + ") OR " + col + " IS NULL)", nil
	case !r.negated:
		return "(" + col + " IN (" + strings.Join(parts, ", ") + "))", nil
	case nullable && !hasNull:
		return "(" + col + " NOT IN (" + strings.Join(parts, ", ") + ") OR " + col + " IS NULL)", nil
	default:
		return "(" + col + " NOT IN (" + strings.Join(parts, ", ") + "))", nil
	}
}

// comparison renders a scalar comparison, op already negated for an equality under negation. A null presence column equals
// null and differs from every value, so `x != v` holds for it and two presence columns compare null-safely (<=>); an ordered
// comparison with it is unknown, as in the evaluator.
func (r *contractRenderer) comparison(op filter.Operator, leftNode, rightNode *contractExpression, lf, rf contractField) (string, error) {
	left, err := r.render(leftNode, rf.kind)
	if err != nil {
		return "", err
	}

	right, err := r.render(rightNode, lf.kind)
	if err != nil {
		return "", err
	}

	sql := "(" + left + " " + comparison(op) + " " + right + ")"
	if !op.IsEquality() {
		if r.negated {
			return "(NOT " + sql + ")", nil
		}

		return sql, nil
	}

	leftNullable, rightNullable := leftNode.IsField() && lf.presence, rightNode.IsField() && rf.presence

	switch {
	case leftNullable && rightNullable && op == filter.OpEqual:
		return "(" + left + " <=> " + right + ")", nil
	case leftNullable && rightNullable:
		return "(NOT (" + left + " <=> " + right + "))", nil
	case op == filter.OpEqual || (!leftNullable && !rightNullable):
		return sql, nil
	}

	col, other, otherKind := left, rightNode, lf.kind
	if rightNullable {
		col, other, otherKind = right, leftNode, rf.kind
	}

	if predicateValue(other) {
		// A predicate used as a value is NULL where the evaluator finds it unknown, which must not match through the NULL
		// test.
		again, err := r.render(other, otherKind)
		if err != nil {
			return "", err
		}

		return "(" + left + " <> " + right + " OR (" + col + " IS NULL AND " + again + " IS NOT NULL))", nil
	}

	return "(" + left + " <> " + right + " OR " + col + " IS NULL)", nil
}

// predicateValue reports whether e is a predicate or a function used as a value, other than size(), which is never NULL.
func predicateValue(e *contractExpression) bool {
	switch e.Tag {
	case filter.NodeKindUnaryOp, filter.NodeKindBinaryOp:
		return true
	case filter.NodeKindCall:
		return e.Op != filter.OpSize
	default:
		return false
	}
}

// oppositeEquality returns != for == and == for !=.
func oppositeEquality(op filter.Operator) filter.Operator {
	if op == filter.OpEqual {
		return filter.OpNotEqual
	}

	return filter.OpEqual
}

func (r *contractRenderer) membership(array, value *contractExpression, f contractField, negate bool) (string, error) {
	col, err := r.render(array, f.kind)
	if err != nil {
		return "", err
	}

	arg, err := r.jsonOperand(value, f.kind)
	if err != nil {
		return "", err
	}

	sql := "JSON_CONTAINS(" + col + ", " + arg + ")"
	if negate {
		sql = "(NOT " + sql + ")"
	}

	return sql, nil // JSON_CONTAINS on a NOT NULL array is never unknown
}

func (r *contractRenderer) elementType(kind sqlbase.Kind) string {
	switch kind {
	case sqlbase.Integer:
		return "BIGINT"
	case sqlbase.Float:
		return "DOUBLE"
	case sqlbase.Numeric:
		typ, _ := r.options.decimalFormat.SQL()
		return typ
	case sqlbase.Timestamp:
		return "DATETIME(" + strconv.Itoa(r.digits) + ")"
	case sqlbase.Boolean:
		return "BOOLEAN"
	case sqlbase.Bytes:
		return "LONGBLOB"
	default:
		return "LONGTEXT CHARACTER SET utf8mb4 COLLATE " + r.options.textCollation
	}
}

func (r *contractRenderer) arrayPredicate(e *contractExpression, f contractField, predicate func(string) (string, error)) (string, error) {
	col, err := r.render(e, f.kind)
	if err != nil {
		return "", err
	}

	var name string
	for {
		name = r.options.arrayAliasPrefix + strconv.Itoa(r.alias)

		r.alias++
		if !r.names[name] {
			r.names[name] = true
			break
		}
	}

	alias := "`" + name + "`"

	pred, err := predicate(alias + ".`element`")
	if err != nil {
		return "", err
	}

	return "EXISTS (SELECT 1 FROM JSON_TABLE(" + col + ", '$[*]' COLUMNS (`element` " + r.elementType(f.kind) + " PATH '$')) AS " + alias +
		" WHERE " + pred + ")", nil
}

// size renders size(). A presence column's NULL has size 0, so size() is never NULL.
func (r *contractRenderer) size(e *contractExpression) (string, error) {
	target := e.Children[0]

	f := field(target)
	if lit, ok := target.Node.(*filter.LiteralNode); ok {
		f.kind = sqltype.InferKind(lit.Value)
	}

	col, err := r.render(target, f.kind)
	if err != nil {
		return "", err
	}

	fn := "CHAR_LENGTH"
	if f.array {
		fn = "JSON_LENGTH"
	} else if f.kind == sqlbase.Bytes {
		fn = "OCTET_LENGTH"
	}

	sql := fn + "(" + col + ")"
	if f.presence {
		sql = "COALESCE(" + sql + ", 0)"
	}

	return sql, nil
}

// substring renders substring() as SUBSTRING, which counts characters from 1. It is NULL where the evaluator finds the
// substring unknown: for a NULL presence column and, behind a CHAR_LENGTH guard, for a text too short to hold it, where
// SUBSTRING would return a truncated value.
func (r *contractRenderer) substring(e *contractExpression) (string, error) {
	target := e.Children[0]

	col, err := r.render(target, field(target).kind)
	if err != nil {
		return "", err
	}

	sql := dialect{}.SubstringExpr(col, e.Span.Start, e.Span.Length)
	if e.Span.Guard == 0 {
		return sql, nil
	}

	return "(CASE WHEN CHAR_LENGTH(" + col + ") >= " + strconv.FormatInt(e.Span.Guard, 10) + " THEN " + sql + " END)", nil
}

// call renders has() and the string functions. A string function finds nothing in a null presence column, so its negation
// holds for it.
func (r *contractRenderer) call(e *contractExpression) (string, error) {
	target := e.Children[0]
	f := field(target)

	switch e.Op {
	case filter.OpHas, filter.OpExists:
		return r.absence(target, r.negated)
	default:
		lit, ok := e.Children[1].Node.(*filter.LiteralNode)
		if !ok {
			return "", filter.ErrUnsupportedOperation
		}

		needle, ok := lit.Value.(string)
		if !ok {
			return "", filter.ErrFieldTypeMismatch
		}

		predicate := func(col string) (string, error) {
			return (dialect{customRegex: !r.policy.UsesRE2Regex()}).StringPredicate(e.Op, col, needle, func(v any) (string, error) { return r.bind(v), nil })
		}
		if f.array {
			sql, err := r.arrayPredicate(target, f, predicate)
			return r.not(sql), err
		}

		col, err := r.render(target, f.kind)
		if err != nil {
			return "", err
		}

		sql, err := predicate(col)

		switch {
		case !r.negated:
			return "(" + sql + ")", err
		case f.presence:
			return "(NOT (" + sql + ") OR " + col + " IS NULL)", err
		default:
			return "(NOT (" + sql + "))", err
		}
	}
}

func (r *contractRenderer) timestampLayout() string {
	layout := "2006-01-02T15:04:05"
	if r.digits > 0 {
		layout += "." + strings.Repeat("0", r.digits)
	}

	return layout + "Z"
}

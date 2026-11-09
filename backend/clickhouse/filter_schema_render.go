// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// sqlTrue and sqlFalse are the constant predicates.
const (
	sqlTrue  = "true"
	sqlFalse = "false"
)

// schemaField is a resolved field operand: its SQL expression and storage shape. nullable is set for a Nullable
// (presence) column and for a JSON path, whose missing or null value reads as NULL.
type schemaField struct {
	expr            string
	kind            sqlbase.Kind
	array, nullable bool
}

// schemaRenderer renders one checked filter from its typed tree. It owns the argument buffer of one Translate call.
type schemaRenderer struct {
	policy  *filter.Policy
	options *schemaOptions
	// digits is the fractional-second digits of the timestamp operands.
	digits int
	args   []any
	// negated marks a predicate under an odd number of `!` operators: negation is pushed down to the predicates, see
	// [schemaRenderer.predicate].
	negated bool
}

// field resolves the SQL expression of a field operand; any other operand has none (an unknown kind).
func (r *schemaRenderer) field(e *schemaExpression) (schemaField, error) {
	if !e.IsField() {
		return schemaField{}, nil
	}

	f, kind := e.Field, e.Field.Kind()

	col, err := sqlbase.QuoteWhole(f.Column(), '`', sqlbase.ClickHouseMaxIdentLen)
	if err != nil {
		return schemaField{}, err
	}

	if !f.IsJSONPath() {
		return schemaField{expr: col, kind: kind, array: f.IsArray(), nullable: f.HasPresence()}, nil
	}

	expr, err := r.jsonPath(col, kind, f.Path())

	return schemaField{expr: expr, kind: kind, nullable: true}, err
}

// jsonPath renders the typed value at path in the JSON text of col. A missing key, a JSON null and a value of
// another type read as NULL. A component that is an integer is an array index: zero-based like PostgreSQL's `#>>`
// (ClickHouse counts from 1), negative from the end.
func (r *schemaRenderer) jsonPath(col string, kind sqlbase.Kind, path []string) (string, error) {
	var b strings.Builder

	b.WriteString(col)

	for _, part := range path {
		b.WriteString(", ")

		if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			if n >= 0 {
				n++
			}

			b.WriteString(strconv.FormatInt(n, 10))

			continue
		}

		b.WriteString(quoteString(part))
	}

	args := b.String()

	// JSONExtract converts between JSON types (the number 5 reads as the String "5", the string "1" and the number 1.5
	// as the Int64 1, the number 1 as true), so each read is guarded by the JSON type of the value: any other type,
	// like a missing key, a JSON null or text that is not JSON (JSONType 'Null'), is NULL.
	typed := func(types, extract string) string {
		return "if(JSONType(" + args + ") IN (" + types + "), " + extract + ", NULL)"
	}

	switch kind {
	case sqlbase.Text:
		return typed("'String'", "JSONExtract("+args+", 'Nullable(String)')"), nil
	case sqlbase.Integer:
		return typed("'Int64'", "JSONExtract("+args+", 'Nullable(Int64)')"), nil
	case sqlbase.Float:
		return typed("'Int64', 'UInt64', 'Double'", "JSONExtract("+args+", 'Nullable(Float64)')"), nil
	case sqlbase.Boolean:
		return typed("'Bool'", "JSONExtract("+args+", 'Nullable(Bool)')"), nil
	case sqlbase.Timestamp:
		return "parseDateTime64BestEffortOrNull(" + typed("'String'", "JSONExtract("+args+", 'Nullable(String)')") + ", " +
			strconv.Itoa(r.digits) + ", 'UTC')", nil
	default:
		return "", unsupported()
	}
}

func (r *schemaRenderer) bind(v any) string {
	if b, ok := v.([]byte); ok {
		v = bytes.Clone(b)
	}

	r.args = append(r.args, v)

	return "?"
}

// literal renders the literal n as a value of kind: a bound argument, explicitly typed for decimals and timestamps.
func (r *schemaRenderer) literal(e *schemaExpression, kind sqlbase.Kind) (string, error) {
	lit, ok := e.Node.(*filter.LiteralNode)
	if !ok {
		return "", filter.ErrUnsupportedOperation
	}

	return r.bindLiteral(lit, kind)
}

// bindLiteral binds lit as a value of kind, explicitly typed for decimals and timestamps.
func (r *schemaRenderer) bindLiteral(lit *filter.LiteralNode, kind sqlbase.Kind) (string, error) {
	v := lit.Value
	if v == nil {
		return "NULL", nil
	}

	if kind == sqlbase.Numeric {
		d, err := r.policy.DecimalOperand(lit, v)
		if err != nil {
			return "", err
		}

		if err = r.options.decimalFormat.Check(d); err != nil {
			return "", err
		}

		return "CAST(" + r.bind(d.String()) + " AS " + r.decimalType() + ")", nil
	}

	if t, ok := v.(time.Time); ok {
		return r.timestamp(t), nil
	}

	return r.bind(v), nil
}

// timestamp binds t truncated to the configured precision and converts it with toDateTime64 in UTC, independent of the
// driver's time formatting and the server time zone.
func (r *schemaRenderer) timestamp(t time.Time) string {
	precision := time.Second
	for range r.digits {
		precision /= 10
	}

	layout := dateTimeLayout
	if r.digits > 0 {
		layout += "." + strings.Repeat("0", r.digits)
	}

	digits := strconv.Itoa(r.digits)

	return "toDateTime64(" + r.bind(t.UTC().Truncate(precision).Format(layout)) + ", " + digits + ", 'UTC')"
}

func (r *schemaRenderer) decimalType() string {
	return "Decimal(" + strconv.Itoa(r.options.decimalFormat.Precision) + ", " + strconv.Itoa(r.options.decimalFormat.Scale) + ")"
}

// render renders n as a value. A predicate used as a value (an operand of a comparison) renders its exact result: true,
// false, or NULL where the default evaluator finds it unknown.
func (r *schemaRenderer) render(e *schemaExpression, hint sqlbase.Kind) (string, error) {
	switch e.Tag {
	case filter.NodeKindIdent:
		f, err := r.field(e)
		return f.expr, err
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

// exact renders the predicate n as a value: true where it holds, false where its negation holds, NULL otherwise.
func (r *schemaRenderer) exact(e *schemaExpression) (string, error) {
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

	return "(CASE WHEN " + holds + " THEN true WHEN " + fails + " THEN false END)", nil
}

// predicate renders the boolean expression n as a WHERE clause that is true exactly where the default evaluator finds n
// true or, negated, false. `!` is pushed down to the predicates (De Morgan over && and ||) instead of rendered as NOT
// around a clause: SQL's NOT keeps an unknown (NULL) result unknown, while the evaluator finds `x == v`, `x in [...]`
// and a string function false for a null presence field, so their negation must match it. Each predicate renders its
// own exact negation.
func (r *schemaRenderer) predicate(e *schemaExpression) (string, error) {
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

// zero returns the value a NOT NULL column of kind stores for an absent Optional field. A timestamp stores the Unix
// epoch: the ClickHouse default of DateTime64 and the proto3 zero Timestamp; Go's zero time is outside the DateTime64
// range.
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
		return time.Unix(0, 0).UTC()
	default:
		return ""
	}
}

// absence renders the null test of n: a Nullable column or JSON path IS [NOT] NULL, the emptiness of an Array, the
// zero-value test of a NOT NULL column. The test is never unknown.
func (r *schemaRenderer) absence(e *schemaExpression, absent bool) (string, error) {
	f, err := r.field(e)
	if err != nil {
		return "", err
	}

	switch {
	case f.nullable && absent:
		return "((" + f.expr + ") IS NULL)", nil
	case f.nullable:
		return "((" + f.expr + ") IS NOT NULL)", nil
	case f.array && absent:
		return "empty(" + f.expr + ")", nil
	case f.array:
		return "notEmpty(" + f.expr + ")", nil
	}

	op := "!="
	if absent {
		op = "="
	}

	val, err := r.bindLiteral(&filter.LiteralNode{Value: zero(f.kind)}, f.kind)

	return "(" + f.expr + " " + op + " " + val + ")", err
}

// nullComparison renders `n == null` (absent) or `n != null`. A column gets [schemaRenderer.absence]; a literal is
// static; size() is never null, and a predicate used as a value is null only where the evaluator finds it unknown, so
// `== null` never holds for either.
func (r *schemaRenderer) nullComparison(e *schemaExpression, absent bool) (string, error) {
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
func (r *schemaRenderer) connective(op filter.Operator) string {
	if (op == filter.OpOr) != r.negated {
		return "OR"
	}

	return "AND"
}

// not wraps sql in NOT under negation, for a predicate that is never unknown.
func (r *schemaRenderer) not(sql string) string {
	if r.negated {
		return "(NOT " + sql + ")"
	}

	return sql
}

// constant renders the constant predicate holds, negated under negation.
func (r *schemaRenderer) constant(holds bool) string {
	if holds != r.negated {
		return sqlTrue
	}

	return sqlFalse
}

func (r *schemaRenderer) binary(e *schemaExpression) (string, error) {
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

	lf, err := r.field(left)
	if err != nil {
		return "", err
	}

	rf, err := r.field(right)
	if err != nil {
		return "", err
	}

	if e.Op == filter.OpIn {
		return r.in(left, right, lf, rf)
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

	if lf.array {
		return r.arrayComparison(right, lf, op)
	}

	return r.comparison(op, left, right, lf, rf)
}

// arrayComparison renders `arr op literal`: `==` and `!=` test membership with has(), an ordered operator holds when
// some element satisfies it. An Array column holds no NULL, so none of them is ever unknown.
func (r *schemaRenderer) arrayComparison(right *schemaExpression, lf schemaField, op filter.Operator) (string, error) {
	if right.Tag != filter.NodeKindLiteral {
		return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "an array is compared with a literal")
	}

	val, err := r.literal(right, lf.kind)
	if err != nil {
		return "", err
	}

	switch op {
	case filter.OpEqual:
		return "has(" + lf.expr + ", " + val + ")", nil
	case filter.OpNotEqual:
		return "(NOT has(" + lf.expr + ", " + val + "))", nil
	default:
		sqlOp, err := sqlbase.ComparisonOperator(op)
		if err != nil {
			return "", err
		}

		return r.not("arrayExists(" + elementParam + " -> " + elementParam + " " + sqlOp + " " + val + ", " + lf.expr + ")"), nil
	}
}

// in renders `x in [...]`, `x in arr` and `arr in [...]`.
func (r *schemaRenderer) in(left, list *schemaExpression, lf, rf schemaField) (string, error) {
	if rf.array {
		// x in arr: the array holds x. An Array holds no NULL element.
		if left.IsNull() {
			return r.constant(false), nil
		}

		val, err := r.literal(left, rf.kind)
		if err != nil {
			return "", err
		}

		return r.not("has(" + rf.expr + ", " + val + ")"), nil
	}

	// Not an array field, so the checker leaves a list.
	parts, hasNull, err := r.listValues(list, lf.kind)
	if err != nil {
		return "", err
	}

	if lf.array {
		// arr in [...]: some element is in the list. NULL elements match nothing.
		if len(parts) == 0 {
			return r.constant(false), nil
		}

		return r.not("hasAny(" + lf.expr + ", [" + strings.Join(parts, ", ") + "])"), nil
	}

	return r.scalarIn(left, lf, parts, hasNull)
}

// listValues renders the literal elements of an `in` list, leaving out the null elements it reports.
func (r *schemaRenderer) listValues(list *schemaExpression, kind sqlbase.Kind) ([]string, bool, error) {
	parts := make([]string, 0, len(list.Children))
	hasNull := false

	for _, child := range list.Children {
		if child.Tag != filter.NodeKindLiteral {
			return nil, false, coreerrs.Wrap(filter.ErrUnsupportedOperation, "an in list holds literals")
		}

		if child.IsNull() {
			hasNull = true
			continue
		}

		part, err := r.render(child, kind)
		if err != nil {
			return nil, false, err
		}

		parts = append(parts, part)
	}

	return parts, hasNull, nil
}

// scalarIn renders `field in [...]` over literals. A null Nullable column is in the list only with a null element, which
// IN never finds, so the null element becomes an IS NULL test; a NOT NULL column holds no null.
func (r *schemaRenderer) scalarIn(left *schemaExpression, lf schemaField, parts []string, hasNull bool) (string, error) {
	col, err := r.render(left, lf.kind)
	if err != nil {
		return "", err
	}

	nullable := left.IsField() && lf.nullable
	matchesNull := nullable && hasNull

	if len(parts) == 0 {
		switch {
		case matchesNull && r.negated, r.negated && left.IsSubstring():
			// A substring() that does not exist is unknown, also under negation.
			return "((" + col + ") IS NOT NULL)", nil
		case matchesNull:
			return "((" + col + ") IS NULL)", nil
		default:
			return r.constant(false), nil
		}
	}

	values := strings.Join(parts, ", ")

	switch {
	case !r.negated && matchesNull:
		return "(" + col + " IN (" + values + ") OR " + col + " IS NULL)", nil
	case !r.negated:
		return "(" + col + " IN (" + values + "))", nil
	case nullable && !hasNull:
		return "(" + col + " NOT IN (" + values + ") OR " + col + " IS NULL)", nil
	case nullable:
		// The null element matches a NULL column, so the negation holds only for a value outside the list.
		return "(" + col + " NOT IN (" + values + ") AND " + col + " IS NOT NULL)", nil
	default:
		return "(" + col + " NOT IN (" + values + "))", nil
	}
}

// comparison renders a scalar comparison, op already negated for an equality under negation. A NULL column or JSON
// path equals null and differs from every value, so `x != v` holds for it and two nullable operands compare
// null-safely; an ordered comparison with it is unknown, as in the evaluator.
func (r *schemaRenderer) comparison(op filter.Operator, leftNode, rightNode *schemaExpression, lf, rf schemaField) (string, error) {
	left, err := r.render(leftNode, rf.kind)
	if err != nil {
		return "", err
	}

	right, err := r.render(rightNode, lf.kind)
	if err != nil {
		return "", err
	}

	sqlOp, err := sqlbase.ComparisonOperator(op)
	if err != nil {
		return "", err
	}

	sql := "(" + left + " " + sqlOp + " " + right + ")"

	if !op.IsEquality() {
		if r.negated {
			return "(NOT " + sql + ")", nil
		}

		return sql, nil
	}

	leftNullable, rightNullable := leftNode.IsField() && lf.nullable, rightNode.IsField() && rf.nullable

	switch {
	case leftNullable && rightNullable && op == filter.OpEqual:
		return "COALESCE(" + left + " = " + right + ", " + left + " IS NULL AND " + right + " IS NULL)", nil
	case leftNullable && rightNullable:
		return "COALESCE(" + left + " != " + right + ", (" + left + " IS NULL) != (" + right + " IS NULL))", nil
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

		return "(" + left + " != " + right + " OR (" + col + " IS NULL AND " + again + " IS NOT NULL))", nil
	}

	return "(" + left + " != " + right + " OR " + col + " IS NULL)", nil
}

// predicateValue reports whether e is a predicate or a function used as a value, other than size(), which is never NULL.
func predicateValue(e *schemaExpression) bool {
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

// size renders size(): code points of a string, bytes of bytes, elements of an array. A NULL column or JSON path has
// size 0, so size() is never NULL.
func (r *schemaRenderer) size(e *schemaExpression) (string, error) {
	target := e.Children[0]

	f, err := r.field(target)
	if err != nil {
		return "", err
	}

	if lit, ok := target.Node.(*filter.LiteralNode); ok {
		f.kind = inferKind(lit.Value)
	}

	col, err := r.render(target, f.kind)
	if err != nil {
		return "", err
	}

	fn := "lengthUTF8"
	if f.array || f.kind == sqlbase.Bytes {
		fn = "length"
	}

	sql := fn + "(" + col + ")"
	if f.nullable {
		sql = "COALESCE(" + sql + ", 0)"
	}

	return sql, nil
}

// substring renders substring() as substringUTF8, which counts code points from 1. It is NULL where the evaluator finds
// the substring unknown: for a NULL column or JSON path and, behind a lengthUTF8 guard, for a text too short to hold it,
// where substringUTF8 would return a truncated value.
func (r *schemaRenderer) substring(e *schemaExpression) (string, error) {
	f, err := r.field(e.Children[0])
	if err != nil {
		return "", err
	}

	sql := filterDialect{}.SubstringExpr(f.expr, e.Span.Start, e.Span.Length)
	if e.Span.Guard == 0 {
		return sql, nil
	}

	return "(CASE WHEN lengthUTF8(" + f.expr + ") >= " + strconv.FormatInt(e.Span.Guard, 10) + " THEN " + sql + " END)", nil
}

// call renders has() and the string functions. A string function finds nothing in a NULL column, so its negation holds
// for it.
func (r *schemaRenderer) call(e *schemaExpression) (string, error) {
	target := e.Children[0]

	f, err := r.field(target)
	if err != nil {
		return "", err
	}

	if e.Op == filter.OpHas || e.Op == filter.OpExists {
		return r.absence(target, r.negated)
	}

	// The checker accepts only a literal argument; a null one is not a string.
	needle, ok := e.Children[1].Value.(string)
	if !ok {
		return "", filter.ErrFieldTypeMismatch
	}

	value := func(v any) (string, error) { return r.bind(v), nil }

	if f.array {
		pred, predErr := filterDialect{}.StringPredicate(e.Op, elementParam, needle, value)
		return r.not("arrayExists(" + elementParam + " -> " + pred + ", " + f.expr + ")"), predErr
	}

	col, err := r.render(target, f.kind)
	if err != nil {
		return "", err
	}

	sql, err := filterDialect{}.StringPredicate(e.Op, col, needle, value)

	switch {
	case !r.negated:
		return "(" + sql + ")", err
	case f.nullable:
		return "(NOT (" + sql + ") OR " + col + " IS NULL)", err
	default:
		return "(NOT (" + sql + "))", err
	}
}

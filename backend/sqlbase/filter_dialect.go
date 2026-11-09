// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

// ValueFunc renders a Go value as SQL text in the translator's active
// mode — a bind placeholder with the value collected into the argument
// slice, or an inline literal.
//
// A [FilterDialect] receives one when rendering a string predicate so it can
// bind the same operand more than once: `endsWith` has no native form in
// MariaDB or PostgreSQL and compiles to `right(col, length(x)) = x`,
// where x appears twice and must therefore be bound twice (three times in
// MariaDB, which also guards the length).
type ValueFunc func(v any) (string, error)

// FilterDialect supplies the per-backend rendering decisions the shared walker
// cannot make on its own. Everything else — the AST walk, the allow-list
// and field-type checks, depth accounting, argument collection, IN lists,
// null handling and the comparison operators — is identical across the
// SQL backends and lives in [FilterTranslator].
//
// Implementations are stateless value types; the walker holds exactly one
// for its lifetime.
type FilterDialect interface {
	// QuoteIdent renders an already-mapped field name as a quoted column
	// reference, rejecting anything that is not a plain identifier with
	// [filter.ErrInvalidExpression]. The dot policy belongs here: a
	// dotted name is a qualified `"table"."column"` in MariaDB and
	// PostgreSQL but a single Nested column in ClickHouse.
	QuoteIdent(name string) (string, error)

	// Placeholder renders the bind marker for the n-th argument, counting
	// from 1. Positional dialects ignore n; PostgreSQL renders "$n".
	Placeholder(n int) string

	// FormatLiteral renders a Go value as SQL text for inline mode.
	// Values the dialect cannot express — a NaN in MariaDB, say — must
	// return [filter.ErrUnsupportedType] rather than an approximation.
	FormatLiteral(v any) (string, error)

	// SizeExpr renders size() over an already-quoted column.
	SizeExpr(col string) string

	// StringPredicate renders contains, startsWith, endsWith or matches
	// over an already-quoted column. The needle arrives raw; the dialect
	// passes it through value to obtain its SQL text, once per occurrence.
	// The regex length cap is already enforced for [filter.OpMatches].
	StringPredicate(op filter.Operator, col, needle string, value ValueFunc) (string, error)
}

// PredicateRenderer renders application predicates ([WithFilterPredicateRenderer]). Field authorization and limits are
// checked before it runs. Implementations must quote identifiers and bind client values through value, and return
// handled=false for nodes the built-in walker should render. It must be safe for concurrent use.
type PredicateRenderer func(node filter.Node, value ValueFunc) (sql string, handled bool, err error)

// ArrayDialect optionally extends a dialect with predicates over the elements of a column declared by
// [filter.WithArrayFields]. Without it the walker rejects array fields with [filter.ErrUnsupportedOperation] rather than
// rendering scalar SQL against an array column.
//
// The walker normalizes every comparison to "element op operand": a literal on the left is moved to the right with the
// converse operator, `x in arr` becomes `arr == x`, `arr in [a, b]` an OR of equalities, and `arr != x` the negation
// of `arr == x` (no element equals x, or the column is NULL). Field operands, a bare array identifier and null element
// tests never reach the dialect.
type ArrayDialect interface {
	// ArrayCompare renders "some element e of col satisfies e op operand", where op is [filter.OpEqual],
	// [filter.OpLT], [filter.OpLTE], [filter.OpGT] or [filter.OpGTE]. operand is the validated raw literal
	// (decimal operands arrive as decimal.Decimal); value renders it and must be called once per SQL occurrence.
	// kind is the element kind declared by [filter.WithFieldTypes]. Element types the backend cannot compare
	// exactly must return [filter.ErrUnsupportedType] or [filter.ErrUnsupportedOperation].
	ArrayCompare(col string, op filter.Operator, operand any, kind filter.FieldKind, value ValueFunc) (string, error)

	// ArrayStringPredicate renders "some element satisfies the string predicate" for contains, startsWith,
	// endsWith and matches, with the same needle contract as [Dialect.StringPredicate].
	ArrayStringPredicate(op filter.Operator, col, needle string, value ValueFunc) (string, error)

	// ArraySize renders size() of an array column as an integer expression; a NULL array has size 0.
	ArraySize(col string) string
}

// EmptyArrayDialect optionally marks a dialect whose array columns cannot hold NULL (a ClickHouse Array): an absent
// array is stored as an empty one. The walker then renders `arr == null` and `arr != null` as emptiness tests,
// has(arr) as non-emptiness, and `arr != x` without the NULL guard it adds for nullable array columns.
type EmptyArrayDialect interface {
	// ArrayEmpty renders the test that the array column col is empty (empty) or holds at least one element.
	ArrayEmpty(col string, empty bool) string
}

// NullSafeDialect optionally extends a dialect with a native null-safe comparison of two field operands, under which
// NULL equals NULL and differs from every value, as in the evaluator. The result must never be unknown. Without it the
// walker renders the portable `COALESCE(a = b, a IS NULL AND b IS NULL)` and
// `COALESCE(a != b, (a IS NULL) != (b IS NULL))`.
type NullSafeDialect interface {
	// NullSafeEqual renders the null-safe equality (equal) or inequality of the already-rendered operands lhs and rhs,
	// such as PostgreSQL's `lhs IS NOT DISTINCT FROM rhs` or MariaDB's `lhs <=> rhs`.
	NullSafeEqual(lhs, rhs string, equal bool) string
}

// DecimalDialect optionally extends a dialect with exact rendering of operands of fields declared
// [filter.FieldKindDecimal]. Without it (and without [WithFilterLiteralEncoder]) [NewFilterTranslator] rejects decimal declarations, because
// binding a decimal through a float would compare inexactly.
type DecimalDialect interface {
	// DecimalValue renders d exactly, typically as a cast of its string spelling passed through value. Values the
	// backend's decimal type cannot hold exactly must return [filter.ErrFieldTypeMismatch].
	DecimalValue(d decimal.Decimal, value ValueFunc) (string, error)
}

// TimestampDialect optionally extends a dialect with the parameterized rendering of a timestamp operand, for a driver
// whose own binding of a time.Time loses precision (clickhouse-go formats a positional time.Time to whole seconds).
// It receives every bound time.Time, after a value encoder that returned one; the inline form keeps
// [FilterDialect.FormatLiteral]. Values the backend's timestamp type cannot hold must return
// [filter.ErrFieldTypeMismatch].
type TimestampDialect interface {
	// TimestampValue renders ts exactly, typically as a conversion of its text passed through value, which binds its
	// argument as is.
	TimestampValue(ts time.Time, value ValueFunc) (string, error)
}

// QualifiedNamesDialect optionally marks a dialect whose [FilterDialect.QuoteIdent] reads a dot in a column name as a
// qualifier: "address.city" is the column city of the table or alias address, as in MariaDB and PostgreSQL. The walker
// then refuses, with [filter.ErrFieldNotAllowed], a dotted field the policy does not declare
// ([filter.Policy.DeclaresField]): one a client composed under a wildcard allow-list or [filter.WithAnyField], which
// could otherwise name a column of another joined table. A dialect that quotes a dotted name as one identifier (a
// ClickHouse Nested column) does not implement it.
type QualifiedNamesDialect interface {
	// QualifiesNames reports whether a dot in a column name separates a qualifier.
	QualifiesNames() bool
}

// unnamedDialect labels the metrics of a dialect without a name.
const unnamedDialect = "sql"

// NamedFilterDialect optionally names a dialect: the name labels the translation metrics of [WithFilterCollector], as in
// filter_<name>_translate_duration_seconds. An unnamed dialect is labeled "sql".
type NamedFilterDialect interface {
	// Name returns the metric label of the dialect, such as "postgres".
	Name() string
}

// SubstringDialect optionally extends a dialect with substring() in comparison-operand position. Without it the
// walker rejects substring() with [filter.ErrUnsupportedOperation].
type SubstringDialect interface {
	// SubstringExpr renders the code-point substring of col starting at the zero-based start, length code points
	// long, or up to the end when length is negative. The walker has already validated the bounds against the
	// expression and guards the row-dependent upper bound with [Dialect.SizeExpr].
	SubstringExpr(col string, start, length int64) string
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// filterDialect implements [sqlbase.FilterDialect] for ClickHouse.
type filterDialect struct{ timestampPrecision int }

// Name labels the translation metrics: filter_clickhouse_translate_duration_seconds ([sqlbase.NamedFilterDialect]).
func (filterDialect) Name() string { return dialectName }

// QuoteIdent renders a column name as a single backtick-quoted
// identifier, dots included: a ClickHouse Nested column is literally
// stored under the name "address.city", so splitting it into a qualified
// reference would name something else.
func (filterDialect) QuoteIdent(name string) (string, error) {
	return sqlbase.QuoteWhole(name, '`', sqlbase.ClickHouseMaxIdentLen)
}

// Placeholder renders ClickHouse's positional bind marker.
func (filterDialect) Placeholder(int) string { return "?" }

// SizeExpr counts Unicode code points in strings and elements in arrays.
// Fields declared by filter.WithArrayFields use [filterDialect.ArraySize].
func (filterDialect) SizeExpr(col string) string {
	return "if(startsWith(toTypeName(" + col + "), 'Array('), length(" + col + "), lengthUTF8(toString(" + col + ")))"
}

// stringPredicates maps the string predicates onto native ClickHouse
// functions. All four take the needle as a plain string, so nothing
// needs pattern escaping — unlike a LIKE-based rendering, a `%` or `_`
// in the operand stays literal. ClickHouse has endsWith(), so the
// operand is bound once.
var stringPredicates = sqlbase.StringPredicates{
	Contains:   "position(%[1]s, %[2]s) > 0",
	StartsWith: "startsWith(%[1]s, %[2]s)",
	EndsWith:   "endsWith(%[1]s, %[2]s)",
	Matches:    "match(%[1]s, %[2]s)",
}

// StringPredicate renders one of the four string predicates.
func (filterDialect) StringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	return sqlbase.RenderStringPredicate(op, col, needle, value, stringPredicates)
}

// elementParam names the lambda parameter of array element predicates.
const elementParam = "_aip_e"

// maxDecimalDigits is the precision of Decimal128, the type decimal operands are cast to.
const maxDecimalDigits = 38

// ArrayCompare tests the elements of an Array column: equality with has(),
// ordered comparisons with arrayExists().
func (filterDialect) ArrayCompare(col string, op filter.Operator, operand any, _ filter.FieldKind, value sqlbase.ValueFunc) (string, error) {
	sqlOp, err := sqlbase.ComparisonOperator(op)
	if err != nil {
		return "", err
	}

	arg, err := value(operand)
	if err != nil {
		return "", err
	}

	if op == filter.OpEqual {
		return "has(" + col + ", " + arg + ")", nil
	}

	return "arrayExists(" + elementParam + " -> " + elementParam + " " + sqlOp + " " + arg + ", " + col + ")", nil
}

// ArrayStringPredicate tests the elements of an Array column with arrayExists().
func (d filterDialect) ArrayStringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	pred, err := d.StringPredicate(op, elementParam, needle, value)
	if err != nil {
		return "", err
	}

	return "arrayExists(" + elementParam + " -> " + pred + ", " + col + ")", nil
}

// ArraySize counts the elements of an Array column.
func (filterDialect) ArraySize(col string) string { return "length(" + col + ")" }

// ArrayEmpty tests an Array column for emptiness. An Array cannot be Nullable,
// so an absent array is stored empty: `arr == null` is empty(arr), and
// `arr != null` and has(arr) are notEmpty(arr).
func (filterDialect) ArrayEmpty(col string, empty bool) string {
	if empty {
		return "empty(" + col + ")"
	}

	return "notEmpty(" + col + ")"
}

// DecimalValue binds the exact decimal spelling and converts it with
// toDecimal128 at the literal's own scale, so nothing is rounded. A value
// with more than 38 significant digits fails with filter.ErrFieldTypeMismatch.
func (filterDialect) DecimalValue(d decimal.Decimal, value sqlbase.ValueFunc) (string, error) {
	text := d.String()
	digits := strings.TrimLeft(strings.NewReplacer("-", "", ".", "").Replace(text), "0")

	scale := 0
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		scale = len(text) - dot - 1
	}

	if len(digits) > maxDecimalDigits || scale > maxDecimalDigits {
		return "", coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "decimal %s exceeds Decimal128", text)
	}

	arg, err := value(text)
	if err != nil {
		return "", err
	}

	return "toDecimal128(" + arg + ", " + strconv.Itoa(scale) + ")", nil
}

// dateTimeLayout is the text of a DateTime64 value without its fraction, which toDateTime64 parses.
const dateTimeLayout = "2006-01-02 15:04:05"

// decimalBase is the base of fractional-second digits.
const decimalBase = 10

// TimestampValue binds the exact UTC text of a timestamp operand and converts it with toDateTime64 at the fewest
// fractional digits that hold it, so nothing is rounded: clickhouse-go would format a bound time.Time to whole
// seconds. A timestamp outside the DateTime64 range (1900-01-01 through 2299-12-31, 2262-04-11 at nanoseconds) fails
// with filter.ErrFieldTypeMismatch rather than being clamped by the server.
func (filterDialect) TimestampValue(ts time.Time, value sqlbase.ValueFunc) (string, error) {
	ts = ts.UTC()

	digits := maxTimestampDigits
	for unit := 1; digits > 0 && ts.Nanosecond()%(unit*decimalBase) == 0; unit *= decimalBase {
		digits--
	}

	if ts.Before(minDateTime64) || !ts.Before(maxDateTime64) || (digits == maxTimestampDigits && !ts.Before(maxDateTime64Nanos)) {
		return "", coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "timestamp %s is outside the DateTime64 range", ts.Format(time.RFC3339Nano))
	}

	layout := dateTimeLayout
	if digits > 0 {
		layout += "." + strings.Repeat("0", digits)
	}

	arg, err := value(ts.Format(layout))
	if err != nil {
		return "", err
	}

	return "toDateTime64(" + arg + ", " + strconv.Itoa(digits) + ", 'UTC')", nil
}

// SubstringExpr renders substring() as substringUTF8, which counts code points from 1. The one-based start of the largest
// bound is capped rather than overflowed; its guard never holds.
func (filterDialect) SubstringExpr(col string, start, length int64) string {
	from := strconv.FormatInt(sqlbase.SubstringStart(start, math.MaxInt64), 10)
	if length < 0 {
		return "substringUTF8(" + col + ", " + from + ")"
	}

	return "substringUTF8(" + col + ", " + from + ", " + strconv.FormatInt(length, 10) + ")"
}

// FormatLiteral renders a Go value as ClickHouse SQL text.
func (d filterDialect) FormatLiteral(v any) (string, error) {
	switch val := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if val {
			return "true", nil
		}

		return "false", nil
	case int64:
		return strconv.FormatInt(val, 10), nil
	case uint64:
		return strconv.FormatUint(val, 10), nil
	case float64:
		return formatFloat(val), nil
	case string:
		return quoteString(val), nil
	case []byte:
		// Hex avoids having to reason about which byte sequences survive
		// a string literal; unhex() yields a ClickHouse String either way.
		return "unhex('" + hex.EncodeToString(val) + "')", nil
	case time.Time:
		layout := dateTimeLayout
		if d.timestampPrecision > 0 {
			layout += "." + strings.Repeat("0", d.timestampPrecision)
		}

		return "toDateTime64('" + val.UTC().Format(layout) + "', " + strconv.Itoa(d.timestampPrecision) + ", 'UTC')", nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// formatFloat renders a float64 as a ClickHouse numeric literal. The
// non-finite values have their own spellings and would otherwise come
// out as Go's "NaN" / "+Inf", which ClickHouse cannot parse.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}

// quoteString wraps a string in single quotes with ClickHouse backslash escaping, in pure ASCII: every byte >= 0x80 is
// written as a \xNN escape, which ClickHouse reads back as that byte. ClickHouse has no client character set, so a raw
// byte is not a breakout risk the way it is in MariaDB and PostgreSQL; the escape keeps the inline output of the three SQL
// backends alike (ASCII only) and safe to pass through any layer that re-encodes the statement.
func quoteString(s string) string {
	const (
		surroundingQuotes = 2 // opening + closing `'`
		hexDigits         = "0123456789abcdef"
	)

	var b strings.Builder
	b.Grow(len(s) + surroundingQuotes)
	b.WriteByte('\'')

	for i := range len(s) {
		switch c := s[i]; {
		case c >= utf8.RuneSelf:
			b.WriteString(`\x`)
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\'':
			b.WriteString(`\'`)
		case c == 0:
			b.WriteString(`\0`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}

	b.WriteByte('\'')

	return b.String()
}

// Ensure filterDialect implements sqlbase.FilterDialect and its optional extensions.
var (
	_ sqlbase.FilterDialect     = filterDialect{}
	_ sqlbase.ArrayDialect      = filterDialect{}
	_ sqlbase.EmptyArrayDialect = filterDialect{}
	_ sqlbase.DecimalDialect    = filterDialect{}
	_ sqlbase.TimestampDialect  = filterDialect{}
	_ sqlbase.SubstringDialect  = filterDialect{}
)

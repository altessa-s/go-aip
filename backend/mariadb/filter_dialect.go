// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

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
	"github.com/altessa-s/go-aip/filter/regexanchor"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// dateTimeLayout renders a [time.Time] as a MariaDB DATETIME literal at
// microsecond precision, the most DATETIME(6) can hold.
const dateTimeLayout = "2006-01-02 15:04:05.000000"

// endsWithOperands is how often the EndsWith template renders its operand:
// the length guard, the suffix size and the comparison.
const endsWithOperands = 3

// dialect implements [sqlbase.FilterDialect] for MariaDB and MySQL.
type dialect struct {
	decimalFormat sqlbase.DecimalFormat
	customRegex   bool
}

// Name labels the translation metrics: filter_mariadb_translate_duration_seconds ([sqlbase.NamedFilterDialect]).
func (dialect) Name() string { return dialectName }

// QuoteIdent renders a column name as a backtick-quoted identifier,
// quoting each dot-separated segment on its own to produce the standard
// qualified form `table`.`column`.
func (dialect) QuoteIdent(name string) (string, error) {
	return sqlbase.QuoteQualified(name, '`', sqlbase.MariaDBMaxIdentLen)
}

// QualifiesNames reports that a dot in a column name separates a table qualifier, so the walker refuses a dotted field
// the policy does not declare.
func (dialect) QualifiesNames() bool { return true }

// Placeholder renders MariaDB's positional bind marker.
func (dialect) Placeholder(int) string { return "?" }

// NullSafeEqual renders the null-safe comparison of two field operands with the <=> operator.
func (dialect) NullSafeEqual(lhs, rhs string, equal bool) string {
	if equal {
		return lhs + " <=> " + rhs
	}

	return "NOT (" + lhs + " <=> " + rhs + ")"
}

// SizeExpr renders size() as CHAR_LENGTH, which counts characters rather
// than the bytes LENGTH would report on a multi-byte charset. Fields
// declared by filter.WithArrayFields use [dialect.ArraySize].
func (dialect) SizeExpr(col string) string { return "CHAR_LENGTH(" + col + ")" }

// stringPredicates maps the string predicates onto MariaDB built-ins.
//
// contains and startsWith both go through LOCATE, which takes the needle
// as a plain string — no LIKE pattern to escape, so a `%` or `_` in the
// operand stays literal. LOCATE takes the needle first, which is why the
// templates index their verbs. startsWith is LOCATE = 1 rather than a
// separate function because MariaDB has none; the two are equivalent,
// including for an empty needle, where LOCATE returns 1.
//
// endsWith has no built-in either and compiles to a suffix comparison
// guarded by a length check. The guard is what keeps it exact: when the
// column is shorter than the needle, RIGHT returns the whole column, and a
// PAD SPACE collation — the default for most MariaDB and MySQL collations,
// utf8mb4_bin included — pads the shorter side with spaces before
// comparing, so "a" would end with "a ". With the guard true, RIGHT
// returns exactly as many characters as the needle has, and padding cannot
// change the outcome. The operand appears three times — to guard, to size
// the suffix, to compare against it. Hence endsWithOperands.
var stringPredicates = sqlbase.StringPredicates{
	Contains:      "LOCATE(%[2]s, %[1]s) > 0",
	StartsWith:    "LOCATE(%[2]s, %[1]s) = 1",
	EndsWith:      "(CHAR_LENGTH(%[1]s) >= CHAR_LENGTH(%[2]s) AND RIGHT(%[1]s, CHAR_LENGTH(%[3]s)) = %[4]s)",
	Matches:       "%[1]s REGEXP %[2]s",
	EndsWithBinds: endsWithOperands,
}

// StringPredicate renders one of the four string predicates.
//
// A matches() pattern has its end-of-text `$` anchors rewritten to `\z`
// first: MariaDB's PCRE and MySQL's ICU engines also match `$` before a
// final newline, which the pattern's RE2 meaning does not allow.
func (d dialect) StringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	if op == filter.OpMatches && !d.customRegex {
		anchored, err := regexanchor.EndOfText(needle)
		if err != nil {
			return "", err
		}

		needle = anchored
	}

	return sqlbase.RenderStringPredicate(op, col, needle, value, stringPredicates)
}

// elementAlias names the JSON_TABLE row source of an array element predicate.
const elementAlias = "_aip_e"

// elementDouble is the JSON_TABLE column type of floating-point elements.
const elementDouble = "DOUBLE"

// ArrayCompare tests the elements of a JSON array column — MariaDB has no
// SQL array type. Equality is JSON_CONTAINS against a one-element array
// built by JSON_ARRAY, which keeps the bound value a JSON string or number;
// booleans are compared with the JSON constants true and false, because
// MariaDB binds a boolean as the integer 1 or 0. JSON_CONTAINS compares
// strings exactly, regardless of collation. Ordered comparisons expand the
// array with JSON_TABLE into a column typed by the declared element kind.
// Timestamps and bytes have no JSON representation and are rejected.
func (d dialect) ArrayCompare(col string, op filter.Operator, operand any, kind filter.FieldKind, value sqlbase.ValueFunc) (string, error) {
	switch v := operand.(type) {
	case time.Time, []byte:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T element of a JSON array", operand)
	case bool:
		if op != filter.OpEqual {
			return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "ordered comparison of boolean array elements")
		}

		if v {
			return "JSON_CONTAINS(" + col + ", 'true')", nil
		}

		return "JSON_CONTAINS(" + col + ", 'false')", nil
	}

	if op == filter.OpEqual {
		arg, err := value(operand)
		if err != nil {
			return "", err
		}

		return "JSON_CONTAINS(" + col + ", JSON_ARRAY(" + arg + "))", nil
	}

	sqlOp, err := sqlbase.ComparisonOperator(op)
	if err != nil {
		return "", err
	}

	typ, err := d.elementType(kind, operand)
	if err != nil {
		return "", err
	}

	arg, err := value(operand)
	if err != nil {
		return "", err
	}

	return elementExists(col, typ, elementAlias+".v "+sqlOp+" "+arg), nil
}

// elementType picks the JSON_TABLE column type for ordered element comparisons: the declared element kind, or the
// operand's type for an undeclared one.
func (d dialect) elementType(kind filter.FieldKind, operand any) (string, error) {
	switch kind {
	case filter.FieldKindInt:
		return "BIGINT", nil
	case filter.FieldKindFloat:
		return elementDouble, nil
	case filter.FieldKindString:
		return "LONGTEXT", nil
	case filter.FieldKindDecimal:
		return d.decimalFormat.SQL()
	case filter.FieldKindUnspecified:
		switch operand.(type) {
		case int64, uint64, float64:
			return elementDouble, nil
		case string:
			return "LONGTEXT", nil
		}
	default:
	}

	return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "ordered comparison of %s JSON array elements", kind)
}

// elementExists renders an EXISTS test over the elements of a JSON array column.
func elementExists(col, typ, pred string) string {
	return "EXISTS (SELECT 1 FROM JSON_TABLE(" + col + ", '$[*]' COLUMNS (v " + typ + " PATH '$')) AS " +
		elementAlias + " WHERE " + pred + ")"
}

// ArrayStringPredicate tests the elements of a JSON array column through JSON_TABLE. The element column uses the
// connection's default collation, as the scalar predicates do.
func (d dialect) ArrayStringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	pred, err := d.StringPredicate(op, elementAlias+".v", needle, value)
	if err != nil {
		return "", err
	}

	return elementExists(col, "LONGTEXT", pred), nil
}

// ArraySize counts the elements of a JSON array column; a NULL column is empty.
func (dialect) ArraySize(col string) string { return "COALESCE(JSON_LENGTH(" + col + "), 0)" }

// DecimalValue binds the exact decimal spelling and casts it to the configured DECIMAL domain
// ([WithFilterDecimalFormat]); a value the cast would round or overflow fails with filter.ErrFieldTypeMismatch.
func (d dialect) DecimalValue(v decimal.Decimal, value sqlbase.ValueFunc) (string, error) {
	if err := d.decimalFormat.Check(v); err != nil {
		return "", err
	}

	typ, err := d.decimalFormat.SQL()
	if err != nil {
		return "", err
	}

	arg, err := value(v.String())
	if err != nil {
		return "", err
	}

	return "CAST(" + arg + " AS " + typ + ")", nil
}

// SubstringExpr renders substring() as SUBSTRING, which counts characters from 1.
// The one-based start of the largest bound is capped rather than overflowed; its guard never holds.
func (dialect) SubstringExpr(col string, start, length int64) string {
	from := strconv.FormatInt(sqlbase.SubstringStart(start, math.MaxInt64), 10)
	if length < 0 {
		return "SUBSTRING(" + col + ", " + from + ")"
	}

	return "SUBSTRING(" + col + ", " + from + ", " + strconv.FormatInt(length, 10) + ")"
}

// FormatLiteral renders a Go value as MariaDB SQL text.
func (dialect) FormatLiteral(v any) (string, error) {
	switch val := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if val {
			return "TRUE", nil
		}

		return "FALSE", nil
	case int64:
		return strconv.FormatInt(val, 10), nil
	case uint64:
		return strconv.FormatUint(val, 10), nil
	case float64:
		return formatFloat(val)
	case string:
		return quoteString(val), nil
	case []byte:
		// X'...' is MariaDB's hex literal; it sidesteps having to reason
		// about which byte sequences survive a string literal.
		return "X'" + hex.EncodeToString(val) + "'", nil
	case time.Time:
		return "'" + val.UTC().Format(dateTimeLayout) + "'", nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// formatFloat renders a float64 as a MariaDB numeric literal. MariaDB has
// no spelling for the non-finite values — DOUBLE cannot hold them at all
// — so those are rejected rather than approximated.
func formatFloat(v float64) (string, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "MariaDB has no literal for %v", v)
	}

	return strconv.FormatFloat(v, 'g', -1, 64), nil
}

// quoteString renders a string as a pure-ASCII MariaDB literal.
//
// A string of ASCII bytes goes in single quotes, doubling the quote and the backslash. Doubling the quote is correct in
// every sql_mode, so the literal cannot be broken out of regardless of server configuration. Doubling the backslash is
// correct under the default mode, where it is an escape character; under NO_BACKSLASH_ESCAPES it is not, and a value
// containing a backslash comes out with it doubled. That costs fidelity, never safety — the quote is already handled —
// and it is the price of rendering without a server round-trip. Use the parameterized form where exact values matter.
//
// A string with any byte >= 0x80 is written as the hex literal _utf8mb4 X'...' instead. The server lexes the statement in
// the connection character set, and in a multibyte one (gbk, big5, sjis, cp932, gb18030) a raw byte >= 0x80 can pair
// with the backslash after it into one character, swallowing an escape and ending the literal early. The hex form has
// nothing to escape and reads the same in every character set; the introducer keeps the value utf8mb4 text whatever the
// connection's character set is, so it compares with a column exactly as a quoted literal would.
func quoteString(s string) string {
	const surroundingQuotes = 2 // opening + closing `'`

	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return "_utf8mb4 X'" + hex.EncodeToString([]byte(s)) + "'"
		}
	}

	var b strings.Builder
	b.Grow(len(s) + surroundingQuotes)
	b.WriteByte('\'')

	for i := range len(s) {
		switch c := s[i]; c {
		case '\'':
			b.WriteString(`''`)
		case '\\':
			b.WriteString(`\\`)
		case 0:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}

	b.WriteByte('\'')

	return b.String()
}

// Ensure dialect implements sqlbase.FilterDialect and its optional extensions.
var (
	_ sqlbase.FilterDialect         = dialect{}
	_ sqlbase.ArrayDialect          = dialect{}
	_ sqlbase.DecimalDialect        = dialect{}
	_ sqlbase.NullSafeDialect       = dialect{}
	_ sqlbase.SubstringDialect      = dialect{}
	_ sqlbase.QualifiedNamesDialect = dialect{}
)

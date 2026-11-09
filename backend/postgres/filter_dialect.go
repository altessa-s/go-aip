// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

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

// dateTimeLayout renders a [time.Time] as a PostgreSQL timestamptz
// literal at microsecond precision, the most the type can hold. The
// trailing +00 is the offset, always UTC here.
const dateTimeLayout = "2006-01-02 15:04:05.000000-07"

// endsWithOperands is how often the EndsWith template renders its operand:
// the suffix size and the comparison.
const endsWithOperands = 2

// filterDialect implements [sqlbase.FilterDialect] for PostgreSQL.
type filterDialect struct{}

// Name labels the translation metrics: filter_postgres_translate_duration_seconds ([sqlbase.NamedFilterDialect]).
func (filterDialect) Name() string { return dialectName }

// QuoteIdent renders a column name as a double-quoted identifier,
// quoting each dot-separated segment on its own to produce the standard
// qualified form "table"."column".
//
// Quoting also pins the case: PostgreSQL folds unquoted identifiers to
// lower case, so a camelCase CEL field maps to a camelCase column, not
// to its lower-cased form. Use [filter.WithFieldMapping] where the table
// actually uses snake_case.
func (filterDialect) QuoteIdent(name string) (string, error) {
	return sqlbase.QuoteQualified(name, '"', sqlbase.PostgresMaxIdentLen)
}

// QualifiesNames reports that a dot in a column name separates a table qualifier, so the walker refuses a dotted field
// the policy does not declare.
func (filterDialect) QualifiesNames() bool { return true }

// Placeholder renders PostgreSQL's numbered bind marker.
func (filterDialect) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

// NullSafeEqual renders the null-safe comparison of two field operands with IS [NOT] DISTINCT FROM.
func (filterDialect) NullSafeEqual(lhs, rhs string, equal bool) string {
	if equal {
		return lhs + " IS NOT DISTINCT FROM " + rhs
	}

	return lhs + " IS DISTINCT FROM " + rhs
}

// SizeExpr renders size() of a text value as length(), which counts
// characters. Fields declared by filter.WithArrayFields use [dialect.ArraySize].
func (filterDialect) SizeExpr(col string) string { return "length(" + col + ")" }

// stringPredicates maps the string predicates onto PostgreSQL built-ins.
//
// contains and startsWith take the needle as a plain string — no LIKE
// pattern to escape, so a `%` or `_` in the operand stays literal.
// starts_with() requires PostgreSQL 11 or newer.
//
// endsWith has no built-in and compiles to a suffix comparison, which
// needs the operand twice — once to size the suffix, once to compare
// against it. Hence endsWithOperands.
var stringPredicates = sqlbase.StringPredicates{
	Contains:      "strpos(%[1]s, %[2]s) > 0",
	StartsWith:    "starts_with(%[1]s, %[2]s)",
	EndsWith:      "right(%[1]s, length(%[2]s)) = %[3]s",
	Matches:       "%[1]s ~ %[2]s",
	EndsWithBinds: endsWithOperands,
}

// StringPredicate renders one of the four string predicates.
func (filterDialect) StringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	return sqlbase.RenderStringPredicate(op, col, needle, value, stringPredicates)
}

// elementAlias names the unnest() row source of an array string predicate.
const elementAlias = "_aip_e"

// ArrayCompare renders "some element satisfies element op operand" as
// `operand op' ANY(col)`, where op' is the converse operator: ANY puts the
// element on the right. A NULL array or a NULL element that decides the
// result yields NULL, which WHERE treats as false.
func (filterDialect) ArrayCompare(col string, op filter.Operator, operand any, _ filter.FieldKind, value sqlbase.ValueFunc) (string, error) {
	sqlOp, err := sqlbase.ComparisonOperator(sqlbase.Converse(op))
	if err != nil {
		return "", err
	}

	arg, err := value(operand)
	if err != nil {
		return "", err
	}

	return arg + " " + sqlOp + " ANY(" + col + ")", nil
}

// ArrayStringPredicate tests the elements of an array column through unnest().
func (d filterDialect) ArrayStringPredicate(op filter.Operator, col, needle string, value sqlbase.ValueFunc) (string, error) {
	pred, err := d.StringPredicate(op, elementAlias+".v", needle, value)
	if err != nil {
		return "", err
	}

	return "EXISTS (SELECT 1 FROM unnest(" + col + ") AS " + elementAlias + "(v) WHERE " + pred + ")", nil
}

// ArraySize counts the elements of an array column; a NULL array is empty.
func (filterDialect) ArraySize(col string) string { return "COALESCE(cardinality(" + col + "), 0)" }

// DecimalValue binds the exact decimal spelling and casts it to NUMERIC,
// which holds any decimal literal exactly.
func (filterDialect) DecimalValue(d decimal.Decimal, value sqlbase.ValueFunc) (string, error) {
	arg, err := value(d.String())
	if err != nil {
		return "", err
	}

	return "CAST(" + arg + " AS NUMERIC)", nil
}

// SubstringExpr renders substring() as substr(), which counts characters
// from 1. substr() takes int4 arguments, so the start and the length are
// capped at math.MaxInt32: a bound that large has a guard (a text of at
// least that many characters) no PostgreSQL text can meet, and an int8
// argument would make the call fail to resolve.
func (filterDialect) SubstringExpr(col string, start, length int64) string {
	from := strconv.FormatInt(sqlbase.SubstringStart(start, math.MaxInt32), 10)
	if length < 0 {
		return "substr(" + col + ", " + from + ")"
	}

	return "substr(" + col + ", " + from + ", " + strconv.FormatInt(min(length, math.MaxInt32), 10) + ")"
}

// FormatLiteral renders a Go value as PostgreSQL SQL text.
func (filterDialect) FormatLiteral(v any) (string, error) {
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
		return formatFloat(val), nil
	case string:
		return quoteString(val), nil
	case []byte:
		// decode() rather than the '\xdead'::bytea form, which relies on
		// standard_conforming_strings being on; this one has no
		// backslash to interpret and is correct either way.
		return "decode('" + hex.EncodeToString(val) + "', 'hex')", nil
	case time.Time:
		return "TIMESTAMP WITH TIME ZONE '" + val.UTC().Format(dateTimeLayout) + "'", nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// formatFloat renders a float64 as a PostgreSQL numeric literal. The
// non-finite values are representable, but only as quoted strings with
// an explicit cast — bare NaN or Infinity would parse as column names.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "'NaN'::double precision"
	case math.IsInf(v, 1):
		return "'Infinity'::double precision"
	case math.IsInf(v, -1):
		return "'-Infinity'::double precision"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}

// quoteString renders a string as an escape-string literal, E'...', in pure ASCII. It is the one text quoter of the
// package: the scalar dialect and the schema translators both render their inline text through it.
//
// The E prefix is what makes this safe without knowing the server's configuration: inside a plain '...' literal the
// backslash is an escape character only when standard_conforming_strings is off, so no single plain-literal escaping is
// correct in both settings. An E-string always interprets backslashes, so one rule covers every server. The quote is
// doubled rather than backslash-escaped: the server refuses \' under backslash_quote=off, and under the default
// safe_encoding whenever the client encoding is a multibyte one.
//
// Every code point above U+007F is written as a \uXXXX or \UXXXXXXXX escape, so the literal holds no byte >= 0x80. The
// server lexes the statement in the client encoding, and in a multibyte one (GBK, BIG5, SJIS, GB18030, UHC) a raw byte >=
// 0x80 can pair with the backslash after it into one character, swallowing an escape and ending the literal early. ASCII
// reads the same in every client encoding, and the server converts each escaped code point to its own encoding.
//
// A NUL byte is escaped to \000 rather than dropped. PostgreSQL text cannot hold one at all, so the server rejects the
// literal — which is the honest outcome, and better than silently truncating the value. A byte that is not part of valid
// UTF-8 is written as \xNN, which the server rejects the same way.
func quoteString(s string) string {
	const prefixAndQuotes = 3 // `E` + opening + closing `'`

	var b strings.Builder
	b.Grow(len(s) + prefixAndQuotes)
	b.WriteString("E'")

	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			writeNonASCII(&b, r, size, c)
			i += size

			continue
		}

		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`''`)
		case 0:
			b.WriteString(`\000`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}

		i++
	}

	b.WriteByte('\'')

	return b.String()
}

// writeNonASCII writes the code point r, decoded from size bytes starting with lead, as an E-string escape: \uXXXX up to
// U+FFFF, \UXXXXXXXX above, and \xNN for a byte that does not start valid UTF-8.
func writeNonASCII(b *strings.Builder, r rune, size int, lead byte) {
	const (
		hexDigits = "0123456789abcdef"
		bmpDigits = 4      // \uXXXX
		maxDigits = 8      // \UXXXXXXXX
		maxBMP    = 0xffff // the last code point \u can spell
		nibble    = 4      // bits per hex digit
	)

	if r == utf8.RuneError && size == 1 {
		b.WriteString(`\x`)
		b.WriteByte(hexDigits[lead>>4])
		b.WriteByte(hexDigits[lead&0xf])

		return
	}

	digits := bmpDigits
	if r > maxBMP {
		digits = maxDigits

		b.WriteString(`\U`)
	} else {
		b.WriteString(`\u`)
	}

	for shift := (digits - 1) * nibble; shift >= 0; shift -= nibble {
		b.WriteByte(hexDigits[(r>>shift)&0xf])
	}
}

// Ensure dialect implements sqlbase.FilterDialect and its optional extensions.
var (
	_ sqlbase.FilterDialect         = filterDialect{}
	_ sqlbase.ArrayDialect          = filterDialect{}
	_ sqlbase.DecimalDialect        = filterDialect{}
	_ sqlbase.NullSafeDialect       = filterDialect{}
	_ sqlbase.SubstringDialect      = filterDialect{}
	_ sqlbase.QualifiedNamesDialect = filterDialect{}
)

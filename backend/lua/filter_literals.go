// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua

import (
	"strconv"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

const (
	luaTrue  = "true"
	luaFalse = "false"
	luaNil   = "nil"

	// Lua type names, as type() returns them.
	luaTypeString  = "string"
	luaTypeNumber  = "number"
	luaTypeBoolean = "boolean"
)

// luaStringEscaper replaces special characters in Lua string literals.
var luaStringEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
	"\x00", `\000`,
)

// formatLiteral converts a Go value to a Lua literal string.
func (t *FilterTranslator) formatLiteral(v any) (string, error) {
	if t.literalEncoder != nil {
		var err error

		v, err = t.literalEncoder(v)
		if err != nil {
			return "", err
		}
	}

	switch val := v.(type) {
	case nil:
		return luaNil, nil
	case bool:
		if val {
			return luaTrue, nil
		}

		return luaFalse, nil
	case int64:
		return strconv.FormatInt(val, 10), nil
	case uint64:
		return strconv.FormatUint(val, 10), nil
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64), nil
	case string:
		return quoteString(val), nil
	case []byte:
		return quoteBytes(val), nil
	case time.Time:
		return formatTimestamp(val), nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// formatTimestamp renders ts as Unix epoch seconds, the number a Lua script compares a stored timestamp with: an integer for a
// whole second (the form the Meilisearch translator emits), otherwise the seconds with their fraction. Lua 5.1 numbers are
// doubles, so a fraction keeps microsecond precision; a different stored unit is chosen with [WithFilterLiteralEncoder].
func formatTimestamp(ts time.Time) string {
	sec, nsec := ts.Unix(), ts.Nanosecond()
	if nsec == 0 {
		return strconv.FormatInt(sec, 10)
	}

	if sec < 0 {
		// Unix floors toward negative infinity, so the fraction does not append to the whole seconds; render sec + nsec/1e9.
		return strconv.FormatFloat(float64(sec)+float64(nsec)/1e9, 'f', -1, 64)
	}

	frac := strings.TrimRight(strconv.FormatInt(int64(nsec)+1e9, 10)[1:], "0")

	return strconv.FormatInt(sec, 10) + "." + frac
}

// quoteString renders s as a double-quoted Lua string literal.
func quoteString(s string) string { return `"` + escapeLuaString(s) + `"` }

// quoteBytes renders b as a double-quoted Lua string literal, which holds arbitrary bytes. Every byte outside printable ASCII,
// and the quote and backslash, is a three-digit decimal escape, so the literal is ASCII and a following digit keeps its value.
func quoteBytes(b []byte) string {
	var sb strings.Builder

	const escapeDigits = 3 // \ddd: a decimal escape below 100 is zero-padded so a following digit keeps its value

	sb.Grow(len(b) + len(`""`))
	sb.WriteByte('"')

	for _, c := range b {
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			sb.WriteByte(c)

			continue
		}

		sb.WriteByte('\\')

		digits := strconv.Itoa(int(c))
		sb.WriteString(strings.Repeat("0", escapeDigits-len(digits)))
		sb.WriteString(digits)
	}

	sb.WriteByte('"')

	return sb.String()
}

// escapeLuaString escapes special characters in a Lua string.
func escapeLuaString(s string) string {
	return luaStringEscaper.Replace(s)
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// MarshalJSON encodes v as an object with its one member.
//
// The bytes are those encoding/json writes for the tagged member (sortValueJSON), HTML escaping included, so tokens keep their
// wire form; they are appended to one buffer instead of being built from a boxed struct.
func (v SortValue) MarshalJSON() ([]byte, error) {
	const maxMemberOverhead = len(`{"m":[,]}`) + 2*len("-9223372036854775808")

	switch v.kind {
	case kindOpaque:
		return nil, fmt.Errorf("%w: opaque value requires a custom position codec", ErrUnsortableValue)
	case kindDecimal:
		text := decimalText(v.decimal())
		return append(append(append(make([]byte, 0, len(text)+len(`{"d":""}`)), `{"d":"`...), text...), `"}`...), nil
	case kindString:
		if !utf8.ValidString(v.s) {
			b := make([]byte, 0, base64.RawURLEncoding.EncodedLen(len(v.s))+len(`{"r":""}`))
			b = append(b, `{"r":"`...)
			b = base64.RawURLEncoding.AppendEncode(b, []byte(v.s))

			return append(b, `"}`...), nil
		}

		b := append(make([]byte, 0, len(v.s)+len(`{"s":""}`)), `{"s":`...)

		return append(appendJSONString(b, v.s), '}'), nil
	case kindInt:
		b := append(make([]byte, 0, maxMemberOverhead), `{"n":`...)
		return append(strconv.AppendInt(b, v.int(), 10), '}'), nil
	case kindFloat:
		f := v.float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return append(append([]byte(`{"x":"`), nonFiniteName(f)...), `"}`...), nil
		}

		b := append(make([]byte, 0, maxMemberOverhead), `{"f":`...)

		return append(appendJSONFloat(b, f), '}'), nil
	case kindBool:
		if v.bool() {
			return []byte(`{"b":true}`), nil
		}

		return []byte(`{"b":false}`), nil
	case kindTime:
		if !rfc3339Year(v.t) {
			b := append(make([]byte, 0, maxMemberOverhead), `{"m":[`...)
			b = append(strconv.AppendInt(b, v.t.Unix(), 10), ',')

			return append(strconv.AppendInt(b, int64(v.t.Nanosecond()), 10), "]}"...), nil
		}

		b := append(make([]byte, 0, len(`{"t":""}`)+len("2006-01-02T15:04:05.999999999Z07:00")), `{"t":"`...)

		b, err := v.t.AppendText(b) // the RFC 3339 text time.Time's MarshalJSON quotes
		if err != nil {
			return nil, fmt.Errorf("json: error calling MarshalJSON for type *time.Time: Time.MarshalJSON: %w", err)
		}

		return append(b, `"}`...), nil
	default:
		return []byte(`{}`), nil
	}
}

// appendJSONFloat appends f as encoding/json writes a float64: the shortest representation, in exponent form outside [1e-6, 1e21)
// with a two-digit exponent minimum dropped.
func appendJSONFloat(b []byte, f float64) []byte {
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}

	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		// clean up e-09 to e-9
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}

	return b
}

// appendJSONString appends the valid UTF-8 string s quoted as encoding/json writes it with HTML escaping: the short escapes, \u00XX
// for other control bytes and for <, > and &, and   and  .
func appendJSONString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"

	b = append(b, '"')
	start := 0

	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= ' ' && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}

			b = append(b, s[start:i]...)

			switch c {
			case '\\', '"':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			}

			i++
			start = i

			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == ' ' || r == ' ' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hex[r&0xF])
			start = i + size
		}

		i += size
	}

	b = append(b, s[start:]...)

	return append(b, '"')
}

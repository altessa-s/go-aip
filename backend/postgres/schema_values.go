// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// DefaultMaxDataBudget bounds values read, decoded and compared per evaluation.
const DefaultMaxDataBudget = 1 << 22

type budget struct {
	options            schemaOptions
	left               int
	jsonbStringCompare func(string, string) int
}

// Spend implements [sqltype.Budget].
func (b *budget) Spend(n int) error {
	if n < 0 || n > b.left {
		return filter.ErrDataLimitExceeded
	}

	b.left -= n

	return nil
}

// TypeError implements [sqltype.Budget].
func (*budget) TypeError() error { return typeError() }

// JSONNull is a JSON null value, distinct from SQL NULL (nil).
type JSONNull struct{}

// numberText spells n as PostgreSQL NUMERIC text.
func numberText(n sqltype.Number) string {
	switch n.Special {
	case sqltype.NegativeInfinity:
		return "-Infinity"
	case sqltype.PositiveInfinity:
		return "Infinity"
	case sqltype.NaN:
		return "NaN"
	default:
	}

	return n.Finite.String()
}

func unwrap(v any, d evaluator.ValueDecoder, b *budget) (any, error) {
	var decode sqltype.Decoder
	if d != nil {
		decode = d.Decode
	}

	return sqltype.Unwrap(v, b.options.maxValueDepth, b, decode)
}

func normalize(v any, k sqlbase.Kind, d evaluator.ValueDecoder, b *budget) (any, error) {
	var err error

	v, err = unwrap(v, d, b)
	if err != nil || v == nil {
		return v, err
	}

	if k == sqlbase.Unknown {
		k = sqltype.InferKind(v)
	}

	switch k {
	case sqlbase.Numeric:
		return sqltype.ParseNumber(v, b)
	case sqlbase.Integer:
		return sqltype.Integer(v, b)
	case sqlbase.Float:
		return sqltype.Float(v, b)
	case sqlbase.Boolean:
		if x, ok := v.(bool); ok {
			return x, nil
		}
	case sqlbase.Text, sqlbase.UUID:
		return normalizeText(v, k, b)
	case sqlbase.Bytes:
		if x, ok := v.([]byte); ok {
			if err := b.Spend(len(x)); err != nil {
				return nil, err
			}

			return bytes.Clone(x), nil
		}
	case sqlbase.Timestamp:
		if x, ok := v.(time.Time); ok {
			return x.UTC().Truncate(b.options.timestampPrecision), nil
		}
	case sqlbase.JSON:
		switch x := v.(type) {
		case string:
			return parseJSON([]byte(x), b)
		case []byte:
			return parseJSON(x, b)
		case json.RawMessage:
			return parseJSON(x, b)
		default:
			return jsonValue(v, b, 0)
		}
	default:
	}

	return nil, typeError()
}

func parseJSON(raw []byte, b *budget) (any, error) {
	if err := b.Spend(len(raw)); err != nil {
		return nil, err
	}

	if !validJSONUnicode(raw) {
		return nil, typeError()
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, coreerrs.Wrap(filter.ErrFieldTypeMismatch, "invalid JSONB")
	}

	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, typeError()
	}

	return jsonValue(v, b, 0)
}

func jsonValue(v any, b *budget, depth int) (any, error) {
	if depth > b.options.maxValueDepth {
		return nil, filter.ErrDataLimitExceeded
	}

	if err := b.Spend(1); err != nil {
		return nil, err
	}

	switch x := v.(type) {
	case nil, JSONNull:
		return JSONNull{}, nil
	case bool:
		return x, nil
	case string:
		return normalize(x, sqlbase.Text, nil, b)
	case sqltype.Number:
		if x.Special != 0 {
			return nil, typeError()
		}

		return x, nil
	case json.Number, float64, int64, int, uint64, decimal.Decimal:
		n, err := sqltype.ParseNumber(x, b)
		if err != nil {
			return nil, err
		}

		if n.Special != 0 {
			return nil, typeError()
		}

		return n, nil
	case []any:
		if err := b.Spend(len(x)); err != nil {
			return nil, err
		}

		out := make([]any, len(x))
		for i, item := range x {
			var err error

			out[i], err = jsonValue(item, b, depth+1)
			if err != nil {
				return nil, err
			}
		}

		return out, nil
	case map[string]any:
		if err := b.Spend(len(x)); err != nil {
			return nil, err
		}

		out := make(map[string]any, len(x))
		for k, item := range x {
			if _, err := normalize(k, sqlbase.Text, nil, b); err != nil {
				return nil, err
			}

			value, err := jsonValue(item, b, depth+1)
			if err != nil {
				return nil, err
			}

			out[k] = value
		}

		return out, nil
	}

	return nil, typeError()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}

	return true
}

func numberCost(n sqltype.Number) int {
	if n.Special != 0 {
		return 1
	}

	return n.Finite.NumDigits() + int(math.Abs(float64(n.Finite.Exponent())))
}

// encoding/json replaces malformed surrogate escapes; PostgreSQL JSONB rejects them.
func validJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}

	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}

		i++
		if i >= len(raw) {
			return false
		}

		if raw[i] != 'u' {
			continue
		}

		if i+4 >= len(raw) {
			return false
		}

		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}

		i += 4

		if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}

		if code < 0xD800 || code > 0xDBFF {
			continue
		}

		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}

		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xDC00 || low > 0xDFFF {
			return false
		}

		i += 6
	}

	return true
}

func (b *budget) compareJSONStrings(a, c string) int {
	if b.jsonbStringCompare != nil {
		return b.jsonbStringCompare(a, c)
	}

	return strings.Compare(a, c)
}

func formatUUID(raw []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}

func normalizeText(v any, k sqlbase.Kind, b *budget) (any, error) {
	if k == sqlbase.UUID {
		if raw, ok := v.([16]byte); ok {
			return formatUUID(raw[:]), nil
		}
	}

	var s string

	switch x := v.(type) {
	case string:
		s = x
	case []byte:
		s = string(x)
	default:
		return nil, typeError()
	}

	if err := b.Spend(len(s)); err != nil {
		return nil, err
	}

	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return nil, typeError()
	}

	if k == sqlbase.UUID {
		s = strings.ReplaceAll(strings.Trim(s, "{}"), "-", "")

		raw, err := hex.DecodeString(s)
		if err != nil || len(raw) != 16 {
			return nil, typeError()
		}

		s = formatUUID(raw)
	}

	return s, nil
}

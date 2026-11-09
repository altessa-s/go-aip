// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

// Lookup reads a declared field, retaining SQL NULL as nil and JSON null as a
// distinct opaque value. present is false only when the column/path is missing.
func (s *Schema) Lookup(policy *filter.Policy, data map[string]any, name string, maxWork int, decoder evaluator.ValueDecoder) (any, bool, error) {
	if err := s.ValidatePolicy(policy); err != nil {
		return nil, false, err
	}

	if !policy.IsFieldAllowed(name) {
		return nil, false, filter.ErrFieldNotAllowed
	}

	f, err := s.field(name)
	if err != nil {
		return nil, false, err
	}

	b := &budget{left: maxWork, options: s.opts}

	value, present, err := readField(f, data, b, decoder)
	if err != nil {
		return nil, false, err
	}

	return publicValue(value), present, nil
}

func readColumn(column string, data map[string]any) (any, bool) {
	if value, ok := data[column]; ok {
		return value, true
	}

	var current any = data
	for part := range strings.SplitSeq(column, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

func readField(f field, data map[string]any, b *budget, d evaluator.ValueDecoder) (any, bool, error) {
	v, ok := readColumn(f.column, data)

	v, err := unwrap(v, d, b)
	if err != nil || v == nil {
		return v, ok, err
	}

	if f.json {
		return readJSONField(f, v, b)
	}

	if f.array {
		if array, ok := v.(ArrayData); ok && !array.Valid {
			return nil, true, nil
		}

		value, arrayErr := readArray(v, f.kind, d, b, 0)

		return value, true, arrayErr
	}

	v, err = normalize(v, f.kind, d, b)

	return v, true, err
}

func readJSONField(f field, v any, b *budget) (any, bool, error) {
	v, err := normalize(v, sqlbase.JSON, nil, b)
	if err != nil {
		return nil, false, err
	}

	for _, part := range f.path {
		if err = b.Spend(1); err != nil {
			return nil, false, err
		}

		var ok bool

		v, ok = jsonStep(v, part)
		if !ok {
			return nil, false, nil
		}
	}

	if f.kind == sqlbase.JSON {
		return v, true, nil
	}

	if _, null := v.(JSONNull); null {
		return nil, true, nil
	}

	text, ok := v.(string)
	if !ok {
		text = jsonText(v)
	}

	v, err = jsonScalar(text, f.kind, b)

	return v, true, err
}

func jsonStep(v any, part string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		v, ok := x[part]
		return v, ok
	case []any:
		i, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}

		if i < 0 {
			i += len(x)
		}

		if i < 0 || i >= len(x) {
			return nil, false
		}

		return x[i], true
	default:
		return nil, false
	}
}

func jsonScalar(text string, k sqlbase.Kind, b *budget) (any, error) {
	var v any = text

	switch k {
	case sqlbase.Integer:
		integer, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, typeError()
		}

		v = integer
	case sqlbase.Boolean:
		parsed, err := parseSQLBool(text)
		if err != nil {
			return nil, err
		}

		v = parsed
	case sqlbase.Bytes:
		parsed, err := parseBytea(text)
		if err != nil {
			return nil, err
		}

		v = parsed
	case sqlbase.Timestamp:
		date, err := parseJSONTimestamp(text)
		if err != nil {
			return nil, typeError()
		}

		v = date
	default:
	}

	return normalize(v, k, nil, b)
}

// parseJSONTimestamp mirrors PostgreSQL's ParseFractionalSecond: parse the
// fraction as float64, then round to even microseconds. Read the original text
// because time.Parse discards fractional digits beyond nanosecond precision.
func parseJSONTimestamp(text string) (time.Time, error) {
	date, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, typeError()
	}

	start := strings.IndexByte(text, '.')
	if start < 0 {
		return date, nil
	}

	end := start + 1
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}

	fraction, err := strconv.ParseFloat(text[start:end], 64)
	if err != nil {
		return time.Time{}, typeError()
	}

	const microsPerSecond = 1_000_000

	micros := time.Duration(math.RoundToEven(fraction * microsPerSecond))

	return date.Truncate(time.Second).Add(micros * time.Microsecond), nil
}

func publicValue(v any) any {
	switch x := v.(type) {
	case sqltype.Number:
		if x.Special != 0 {
			return numberText(x)
		}

		return x.Finite
	case arrayValue:
		elements := make([]any, len(x.values))
		for i, item := range x.values {
			elements[i] = publicValue(item)
		}

		return ArrayData{Elements: elements, Dimensions: convertDimensions[Dimension](x.dimensions), Valid: true}
	case []any:
		for i, item := range x {
			x[i] = publicValue(item)
		}

		return x
	case map[string]any:
		for key, item := range x {
			x[key] = publicValue(item)
		}

		return x
	default:
		return v
	}
}

//nolint:gocritic // PostgreSQL accepts prefixes of the canonical true/false spellings.
func parseSQLBool(text string) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(text))
	if value == "" {
		return false, typeError()
	}

	switch {
	case value == "1" || strings.HasPrefix("true", value) || strings.HasPrefix("yes", value):
		return true, nil
	case value == "0" || strings.HasPrefix("false", value) || strings.HasPrefix("no", value):
		return false, nil
	case len(value) > 1 && strings.HasPrefix("on", value):
		return true, nil
	case len(value) > 1 && strings.HasPrefix("off", value):
		return false, nil
	default:
		return false, typeError()
	}
}

func parseBytea(text string) ([]byte, error) {
	if strings.HasPrefix(text, `\x`) {
		return parseHexBytea(text[2:])
	}

	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			out = append(out, text[i])
			continue
		}

		i++
		if i == len(text) {
			return nil, typeError()
		}

		if text[i] == '\\' {
			out = append(out, '\\')
			continue
		}

		const octalDigits = 3
		if i+octalDigits > len(text) {
			return nil, typeError()
		}

		if text[i] < '0' || text[i] > '3' || text[i+1] < '0' || text[i+1] > '7' || text[i+2] < '0' || text[i+2] > '7' {
			return nil, typeError()
		}

		n, err := strconv.ParseUint(text[i:i+octalDigits], 8, 8)
		if err != nil {
			return nil, typeError()
		}

		out = append(out, byte(n))
		i += octalDigits - 1
	}

	return out, nil
}

func parseHexBytea(text string) ([]byte, error) {
	const pairWidth = 2

	out := make([]byte, 0, len(text)/pairWidth)
	for i := 0; i < len(text); {
		if strings.ContainsRune(" \t\r\n\f\v", rune(text[i])) {
			i++
			continue
		}

		if i+pairWidth > len(text) {
			return nil, typeError()
		}

		if !hexDigit(text[i]) || !hexDigit(text[i+1]) {
			return nil, typeError()
		}

		n, err := strconv.ParseUint(text[i:i+pairWidth], 16, 8)
		if err != nil {
			return nil, typeError()
		}

		out = append(out, byte(n))
		i += pairWidth
	}

	return out, nil
}

func hexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

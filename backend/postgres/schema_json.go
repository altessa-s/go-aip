// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
)

const (
	jsonNullRank = iota
	jsonStringRank
	jsonNumberRank
	jsonBoolRank
	jsonArrayRank
	jsonObjectRank
)

func jsonRank(v any) int {
	switch v.(type) {
	case JSONNull:
		return jsonNullRank
	case string:
		return jsonStringRank
	case sqltype.Number:
		return jsonNumberRank
	case bool:
		return jsonBoolRank
	case []any:
		return jsonArrayRank
	case map[string]any:
		return jsonObjectRank
	}

	return -1
}

func jsonKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, func(a, b string) int {
		if n := cmp.Compare(len(a), len(b)); n != 0 {
			return n
		}

		return strings.Compare(a, b)
	})

	return keys
}

func compareJSON(a, c any, b *budget) (int, error) { return compareJSONAt(a, c, b, true) }

func compareJSONAt(a, c any, b *budget, top bool) (int, error) {
	if top {
		x, xok := a.([]any)
		y, yok := c.([]any)

		emptyA, emptyB := xok && len(x) == 0, yok && len(y) == 0
		if emptyA != emptyB {
			if emptyA {
				return -1, nil
			}

			return 1, nil
		}
	}

	if err := b.Spend(1); err != nil {
		return 0, err
	}

	if n := cmp.Compare(jsonRank(a), jsonRank(c)); n != 0 {
		return n, nil
	}

	switch x := a.(type) {
	case JSONNull:
		return 0, nil
	case sqltype.Number:
		y, ok := c.(sqltype.Number)
		if !ok {
			return 0, typeError()
		}

		if err := b.Spend(numberCost(x) + numberCost(y)); err != nil {
			return 0, err
		}

		return sqltype.CompareNumber(x, y), nil
	case string:
		y, ok := c.(string)
		if !ok {
			return 0, typeError()
		}

		if err := b.Spend(len(x) + len(y)); err != nil {
			return 0, err
		}

		return b.compareJSONStrings(x, y), nil
	case bool:
		return compareValue(a, c, sqlbase.Boolean, b)
	case []any:
		y, ok := c.([]any)
		if !ok {
			return 0, typeError()
		}

		return compareJSONLists(x, y, b)
	case map[string]any:
		y, ok := c.(map[string]any)
		if !ok {
			return 0, typeError()
		}

		return compareJSONObjects(x, y, b)

	default:
		return 0, typeError()
	}
}

func jsonText(v any) string {
	switch x := v.(type) {
	case JSONNull:
		return "null"
	case sqltype.Number:
		if x.Finite.Exponent() < 0 {
			return x.Finite.StringFixed(-x.Finite.Exponent())
		}

		return numberText(x)
	case string:
		return quoteJSONText(x)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = jsonText(item)
		}

		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		parts := make([]string, 0, len(x))
		for _, k := range jsonKeys(x) {
			parts = append(parts, jsonText(k)+": "+jsonText(x[k]))
		}

		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return "null"
	}
}

//nolint:nilnil // SQL NULL and JSON null remain distinct.
func jsonPredicate(a, c any, b *budget) (any, error) {
	if a == nil {
		return false, nil // a NULL target contains nothing, as for a string function
	}

	if c == nil {
		return nil, nil
	}

	return jsonContains(a, c, b)
}

func jsonContains(a, c any, b *budget) (bool, error) {
	if err := b.Spend(1); err != nil {
		return false, err
	}

	switch right := c.(type) {
	case map[string]any:
		left, ok := a.(map[string]any)
		if !ok {
			return false, nil
		}

		for key, want := range right {
			value, found := left[key]
			if !found || jsonRank(value) != jsonRank(want) {
				return false, nil
			}

			match, err := jsonContains(value, want, b)
			if err != nil || !match {
				return match, err
			}
		}

		return true, nil
	case []any:
		left, ok := a.([]any)
		if !ok {
			return false, nil
		}

		for _, want := range right {
			match, err := jsonArrayContains(left, want, b)
			if err != nil || !match {
				return match, err
			}
		}

		return true, nil
	default:
		if left, ok := a.([]any); ok {
			return jsonArrayContains(left, c, b)
		}

		order, err := compareJSON(a, c, b)

		return order == 0, err
	}
}

func jsonArrayContains(left []any, want any, b *budget) (bool, error) {
	for _, v := range left {
		// Primitive membership never searches recursively inside nested arrays.
		if jsonRank(v) != jsonRank(want) {
			continue
		}

		match, err := jsonContains(v, want, b)
		if err != nil || match {
			return match, err
		}
	}

	return false, nil
}

func compareJSONLists(x, y []any, b *budget) (int, error) {
	if n := cmp.Compare(len(x), len(y)); n != 0 {
		return n, nil
	}

	for i := range x {
		n, err := compareJSONAt(x[i], y[i], b, false)
		if err != nil || n != 0 {
			return n, err
		}
	}

	return 0, nil
}

func compareJSONObjects(x, y map[string]any, b *budget) (int, error) {
	if n := cmp.Compare(len(x), len(y)); n != 0 {
		return n, nil
	}

	kx, ky := jsonKeys(x), jsonKeys(y)
	for i, k := range kx {
		if err := b.Spend(len(k) + len(ky[i])); err != nil {
			return 0, err
		}

		if n := b.compareJSONStrings(k, ky[i]); n != 0 {
			return n, nil
		}

		n, err := compareJSONAt(x[k], y[ky[i]], b, false)
		if err != nil || n != 0 {
			return n, err
		}
	}

	return 0, nil
}

// PostgreSQL JSONB text escapes controls, quotes and backslashes, but not HTML or Unicode separators.
func quoteJSONText(s string) string {
	const (
		hexadecimal         = "0123456789abcdef"
		firstPrintableASCII = 0x20
	)

	var out strings.Builder
	out.WriteByte('"')

	for i := range len(s) {
		c := s[i]
		switch c {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < firstPrintableASCII {
				out.WriteString(`\u00`)
				out.WriteByte(hexadecimal[c>>4])
				out.WriteByte(hexadecimal[c&0xf])
			} else {
				out.WriteByte(c)
			}
		}
	}

	out.WriteByte('"')

	return out.String()
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy

import (
	"math"

	"github.com/altessa-s/go-aip/internal/literalvalue"
)

// NormalizeEnumValue accepts immutable stored values and normalizes integers fitting int64.
func NormalizeEnumValue(v any) (stored any, isString, ok bool) {
	switch n := v.(type) {
	case string:
		return n, true, true
	case int:
		return int64(n), false, true
	case int8:
		return int64(n), false, true
	case int16:
		return int64(n), false, true
	case int32:
		return int64(n), false, true
	case int64:
		return n, false, true
	case uint8:
		return int64(n), false, true
	case uint16:
		return int64(n), false, true
	case uint32:
		return int64(n), false, true
	case uint:
		if uint64(n) > math.MaxInt64 {
			return n, false, true
		}

		return int64(n), false, true //nolint:gosec // bounded above
	case uint64:
		if n > math.MaxInt64 {
			return n, false, true
		}

		return int64(n), false, true //nolint:gosec // bounded above
	default:
		return v, false, v != nil && literalvalue.Immutable(v)
	}
}

// IntegerValue returns the shared signed-integer enum representation. Other
// comparable values remain usable by parser codecs but are not listing integers.
func IntegerValue(v any) (int64, bool) {
	n, _, ok := NormalizeEnumValue(v)
	if !ok {
		return 0, false
	}

	i, ok := n.(int64)

	return i, ok
}

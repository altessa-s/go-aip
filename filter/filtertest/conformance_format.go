// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FormatSQL renders a parameterized query and its bound arguments as one string for a conformance expectation:
// `query | [arg, arg]`, or the query alone when there are no arguments. Arguments are typed by their spelling: strings quoted,
// bytes as b"…", timestamps as timestamp("RFC 3339"), float64 with a decimal point or exponent, other [fmt.Stringer] values
// (such as decimal.Decimal) as Type(text).
func FormatSQL(query string, args []any) string {
	if len(args) == 0 {
		return query
	}

	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = FormatValue(a)
	}

	return query + " | [" + strings.Join(parts, ", ") + "]"
}

// FormatValue renders one bound argument or literal value the way [FormatSQL] does.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(x)
	case []byte:
		return "b" + strconv.Quote(string(x))
	case time.Time:
		return `timestamp("` + x.UTC().Format(time.RFC3339Nano) + `")`
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eEn") { // n: NaN, Inf
			s += ".0"
		}

		return s
	case int, int64, uint64, bool:
		return fmt.Sprint(x)
	case fmt.Stringer:
		return fmt.Sprintf("%T(%s)", v, x)
	default:
		return fmt.Sprintf("%#v", v)
	}
}

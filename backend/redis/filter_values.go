// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// fieldType returns the FieldType for a given field name.
// Defaults to FieldTypeTag if not found in schema.
func (t *FilterTranslator) fieldType(field string) FieldType {
	if ft, ok := t.schema.Get(field); ok {
		return ft
	}

	return FieldTypeTag
}

// formatLiteral converts a Go value to a string representation for RediSearch.
//
// Null stays nil: RediSearch has no null literal, and the callers turn it
// into an ismissing() presence test or reject it. A timestamp becomes Unix
// epoch seconds, the unit the Meilisearch translator uses too, with the
// sub-second part kept as an exact decimal fraction so an ordered comparison
// against an integer epoch keeps its boundary; store a timestamp as a NUMERIC
// epoch in seconds, or convert it with WithFilterLiteralEncoder for another unit.
// Bytes become their UTF-8 text, escaped like any string.
func (t *FilterTranslator) formatLiteral(v any) (any, error) {
	if t.literalEncoder != nil {
		var err error

		v, err = t.literalEncoder(v)
		if err != nil {
			return nil, err
		}
	}

	switch val := v.(type) {
	case nil:
		return nil, nil //nolint:nilnil // nil is the null literal, not a missing result
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
		return strconv.FormatFloat(val, 'g', -1, 64), nil
	case string:
		return val, nil
	case []byte:
		if !utf8.Valid(val) {
			return nil, coreerrs.Wrap(filter.ErrUnsupportedType, "bytes literal is not valid UTF-8")
		}

		return string(val), nil
	case time.Time:
		return epochSeconds(val), nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// epochSeconds renders ts as Unix epoch seconds, with the nanoseconds as an
// exact decimal fraction without trailing zeros.
func epochSeconds(ts time.Time) string {
	const nanosPerSecond = int64(time.Second)

	sec, nsec := ts.Unix(), int64(ts.Nanosecond())
	if nsec == 0 {
		return strconv.FormatInt(sec, 10)
	}

	sign := ""
	if sec < 0 {
		// -1.75 is Unix() -2 with 250ms: borrow one second so the fraction
		// reads as the magnitude.
		sign, sec, nsec = "-", -(sec + 1), nanosPerSecond-nsec
	}

	frac := strings.TrimRight(fmt.Sprintf("%09d", nsec), "0")

	return sign + strconv.FormatInt(sec, 10) + "." + frac
}

// formatNumericValue formats a value for RediSearch numeric range syntax. The
// value is spliced into `[min max]` unquoted, so only a finite number may reach
// it: a string such as `0] | @secret:[0 +inf` would close the range and open a
// clause against another field. Literals arrive already rendered as strings
// (see formatLiteral), so a string is parsed and re-rendered from the parsed
// number, and anything that does not parse is rejected.
func formatNumericValue(value any) (string, error) {
	switch v := value.(type) {
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "non-finite numeric value %v", v)
		}

		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return formatNumericValue(i)
		}

		if u, err := strconv.ParseUint(v, 10, 64); err == nil {
			return formatNumericValue(u)
		}

		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return formatNumericValue(f)
		}

		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "numeric field requires a number, got %q", v)
	default:
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "numeric field requires a number, got %T", value)
	}
}

// escapeQueryValue escapes the RediSearch special characters in a
// caller-supplied value, for TAG and TEXT alike.
//
// TEXT values were interpolated raw until a fuzz target showed what that
// allows: RediSearch has no quoting to hide inside, so a value carrying
// `) | (@other:...` closed its own group and opened a clause against another
// field. Escaping a space turns a multi-word value into one term rather than an
// implicit AND of two, which is the fail-closed reading of a caller's literal.
func (t *FilterTranslator) escapeQueryValue(s string) string {
	return escapeValue(s)
}

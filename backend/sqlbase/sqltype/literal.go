// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

// MaxIntegerDigits and MaxFractionDigits are the digit bounds of an unconstrained SQL NUMERIC, PostgreSQL's limits:
// digits before and after the decimal point.
const (
	MaxIntegerDigits  = 131072
	MaxFractionDigits = 16383
)

// The special values of a [Number].
const (
	NegativeInfinity = -1
	PositiveInfinity = 1
	NaN              = 2
)

// Budget charges the work spent reading literal values and builds the dialect's operand type error.
type Budget interface {
	// Spend charges n units, failing with [filter.ErrDataLimitExceeded] once the budget is spent or for a negative n.
	Spend(n int) error
	// TypeError returns the dialect's error for a value outside its operand type.
	TypeError() error
}

// Decoder converts a driver-owned value to a native value; nil is SQL NULL.
type Decoder func(any) (any, error)

// Number is an exact SQL NUMERIC value: a finite decimal, or a special value ([NegativeInfinity], [PositiveInfinity],
// [NaN]) with a zero Finite.
type Number struct {
	Finite  decimal.Decimal
	Special int
}

// InferKind returns the operand kind of a Go literal value, [sqlbase.Unknown] for a type without one.
func InferKind(v any) sqlbase.Kind {
	switch v.(type) {
	case bool:
		return sqlbase.Boolean
	case string:
		return sqlbase.Text
	case []byte:
		return sqlbase.Bytes
	case time.Time:
		return sqlbase.Timestamp
	case decimal.Decimal, json.Number:
		return sqlbase.Numeric
	case float64, float32:
		return sqlbase.Float
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return sqlbase.Integer
	}

	return sqlbase.Unknown
}

// Unwrap follows pointers and interfaces, and the conversions of decode when it is not nil, to the value they hold,
// charging one unit per step. A nil value at any step is SQL NULL (nil). More than maxDepth steps fail with
// [filter.ErrDataLimitExceeded].
//
//nolint:nilnil // nil is a successful SQL NULL conversion.
func Unwrap(v any, maxDepth int, b Budget, decode Decoder) (any, error) {
	for range maxDepth {
		if err := b.Spend(1); err != nil {
			return nil, err
		}

		if v == nil {
			return nil, nil
		}

		rv := reflect.ValueOf(v)
		if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil() {
			return nil, nil
		}

		if decode != nil {
			next, err := decode(v)
			if err != nil {
				return nil, err
			}

			if next == nil {
				return nil, nil
			}

			if reflect.TypeOf(next) != reflect.TypeOf(v) {
				v = next
				continue
			}

			v = next
			rv = reflect.ValueOf(v)
		}

		if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			v = rv.Elem().Interface()
			continue
		}

		return v, nil
	}

	return nil, filter.ErrDataLimitExceeded
}

// ParseNumber reads v as a [Number]: a Number, a decimal, a JSON number, a decimal string (also "NaN" and the
// infinities) or a Go integer or float. The digits read are charged to b; a value outside [MaxIntegerDigits] and
// [MaxFractionDigits] or not a number fails with b's type error.
func ParseNumber(v any, b Budget) (Number, error) {
	var s string

	switch x := v.(type) {
	case Number:
		return x, nil
	case decimal.Decimal:
		// Check exponent before String() can allocate an enormous zero suffix.
		if x.Exponent() > MaxIntegerDigits || x.Exponent() < -MaxFractionDigits {
			return Number{}, b.TypeError()
		}

		if err := b.Spend(x.NumDigits() + int(math.Abs(float64(x.Exponent())))); err != nil {
			return Number{}, err
		}

		s = x.String()
	case json.Number:
		s = string(x)
	case string:
		s = x
	case int64:
		s = strconv.FormatInt(x, 10)
	case uint64:
		s = strconv.FormatUint(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'g', -1, 64)
	default:
		s = formatNumber(v)
		if s == "" {
			return Number{}, b.TypeError()
		}
	}

	if err := b.Spend(len(s)); err != nil {
		return Number{}, err
	}

	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "nan":
		return Number{Special: NaN}, nil
	case "infinity", "+infinity", "+inf", "inf":
		return Number{Special: PositiveInfinity}, nil
	case "-infinity", "-inf":
		return Number{Special: NegativeInfinity}, nil
	default:
	}

	if len(s) > MaxIntegerDigits+MaxFractionDigits+32 {
		return Number{}, b.TypeError()
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return Number{}, b.TypeError()
	}

	digits := int64(d.NumDigits())

	exp := int64(d.Exponent())
	if digits+exp > MaxIntegerDigits || exp < -MaxFractionDigits {
		return Number{}, b.TypeError()
	}

	if err := b.Spend(d.NumDigits() + int(math.Abs(float64(d.Exponent())))); err != nil {
		return Number{}, err
	}

	return Number{Finite: d}, nil
}

// formatNumber formats a Go integer or float of any named type, "" for another type.
func formatNumber(v any) string {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return ""
	}

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, 64)
	default:
		return ""
	}
}

// CompareNumber orders two numbers as SQL NUMERIC does: -Infinity, the finite values, +Infinity, then NaN.
func CompareNumber(a, b Number) int {
	if a.Special != 0 || b.Special != 0 {
		if a.Special < b.Special {
			return -1
		}

		if a.Special > b.Special {
			return 1
		}

		return 0
	}

	return a.Finite.Cmp(b.Finite)
}

// Integer reads v as a BIGINT: an integral [Number] within the int64 range, returned as an int64.
func Integer(v any, b Budget) (any, error) {
	n, err := ParseNumber(v, b)
	if err != nil {
		return nil, err
	}

	if n.Special != 0 || !n.Finite.Equal(n.Finite.Truncate(0)) {
		return nil, b.TypeError()
	}

	if n.Finite.LessThan(decimal.NewFromInt(math.MinInt64)) || n.Finite.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return nil, b.TypeError()
	}

	return n.Finite.IntPart(), nil
}

// Float reads v as a DOUBLE: a float as it is, a [Number] rounded to the nearest float64. A finite number that
// overflows to an infinity or underflows to zero fails with b's type error.
func Float(v any, b Budget) (any, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	}

	n, err := ParseNumber(v, b)
	if err != nil {
		return nil, err
	}

	switch n.Special {
	case NegativeInfinity:
		return math.Inf(-1), nil
	case PositiveInfinity:
		return math.Inf(1), nil
	case NaN:
		return math.NaN(), nil
	default:
		f, _ := n.Finite.Float64()
		if math.IsInf(f, 0) || f == 0 && !n.Finite.IsZero() {
			return nil, b.TypeError()
		}

		return f, nil
	}
}

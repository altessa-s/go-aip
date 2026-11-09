// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"reflect"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

func (e *contractEvaluator) field(ref *fieldRef, data map[string]any) (any, error) {
	value, present, err := e.reader.Lookup(data, ref.name)
	if err != nil {
		return nil, err
	}

	if ref.info.Array {
		return e.arrayField(ref, value, present)
	}

	return e.scalarField(ref, value, present)
}

// arrayField normalizes the stored value of an array field: absent reads as an empty array, timestamps are truncated to the
// schema's precision and decimals are parsed.
func (e *contractEvaluator) arrayField(ref *fieldRef, value any, present bool) (any, error) {
	value, err := e.normalizeArray(value)
	if err != nil {
		return nil, err
	}

	if ref.info.Kind == filter.FieldKindTimestamp {
		items := elements(value)
		for i, item := range items {
			if at, ok := item.(time.Time); ok {
				items[i] = at.UTC().Truncate(e.precision)
			}
		}

		value = items
	}

	if !present || value == nil {
		return []any{}, nil
	}

	switch ref.info.Kind {
	case filter.FieldKindFloat:
		for _, item := range elements(value) {
			if err := checkFinite(ref, item); err != nil {
				return nil, err
			}
		}
	case filter.FieldKindDecimal:
		return e.normalizeDecimalArray(value)
	default:
	}

	return value, nil
}

// scalarField normalizes the stored value of a scalar field: an absent optional field reads as its zero value, an absent presence
// field as null, timestamps are truncated to the schema's precision and decimals are parsed.
func (e *contractEvaluator) scalarField(ref *fieldRef, value any, present bool) (any, error) {
	value, err := e.normalizeScalar(value)
	if err != nil {
		return nil, err
	}

	field := ref.info
	if field.Kind == filter.FieldKindBytes && !field.Presence && value == nil {
		return []byte{}, nil
	}

	if field.Optional && (!present || value == nil) {
		return ref.zero, nil
	}

	if field.Presence && (!present || normalizeScalar(value) == nil) {
		return nil, nil //nolint:nilnil // an absent presence field reads as null, not an error
	}

	switch field.Kind {
	case filter.FieldKindTimestamp:
		if at, ok := value.(time.Time); ok {
			return at.UTC().Truncate(e.precision), nil
		}
	case filter.FieldKindFloat:
		if err := checkFinite(ref, value); err != nil {
			return nil, err
		}
	case filter.FieldKindDecimal:
		if value != nil {
			return e.policy.DecimalOperand(nil, value)
		}
	default:
	}

	return value, nil
}

// checkFinite reports a numeric value that is NaN or infinite.
func checkFinite(ref *fieldRef, value any) error {
	if number, ok := numericFloat(value); ok && !finite(number) {
		return &nonFiniteFloatError{field: ref.name}
	}

	return nil
}

func (e *contractEvaluator) normalizeDecimalArray(value any) ([]any, error) {
	items := elements(value)
	for i, item := range items {
		if item == nil {
			continue
		}

		number, err := e.policy.DecimalOperand(nil, item)
		if err != nil {
			return nil, err
		}

		items[i] = number
	}

	return items, nil
}

func normalizeScalar(value any) any {
	if normalized, ok := normalizeBuiltin(value); ok {
		return normalized
	}

	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil
		}

		rv = rv.Elem()
	}

	if !rv.IsValid() {
		return nil
	}

	if timestamp, ok := reflect.TypeAssert[time.Time](rv); ok {
		return timestamp.UTC()
	}

	switch rv.Kind() {
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			if rv.IsNil() {
				return nil
			}

			return rv.Bytes()
		}
	default:
		// Any other kind is read as the value it holds.
	}

	return rv.Interface()
}

// normalizeBuiltin is normalizeScalar for the unnamed built-in types, without reflection. A value already in normal form is
// returned as it is, so it is not boxed again.
//
//nolint:gocyclo // one case per built-in type
func normalizeBuiltin(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case string, bool, int64, uint64, float64, decimal.Decimal:
		return value, true
	case time.Time:
		if utc := v.UTC(); utc != v {
			return utc, true
		}

		return value, true
	case []byte:
		if v == nil {
			return nil, true
		}

		return value, true
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case uint:
		return uint64(v), true
	case uint8:
		return uint64(v), true
	case uint16:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case float32:
		return float64(v), true
	default:
		return nil, false
	}
}

func elements(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Array && rv.Kind() != reflect.Slice) ||
		(rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8) {
		return nil
	}

	out := make([]any, rv.Len())
	for i := range rv.Len() {
		out[i] = normalizeScalar(rv.Index(i).Interface())
	}

	return out
}

//nolint:nilnil // A nil scalar is a valid stored null, not a normalization failure.
func (e *contractEvaluator) normalizeScalar(value any) (any, error) {
	if normalized, ok := normalizeBuiltin(value); ok {
		// Not a pointer: nothing to follow or charge.
		return normalized, nil
	}

	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if !e.charge(1) {
			return nil, filter.ErrDataLimitExceeded
		}

		if rv.IsNil() {
			return nil, nil
		}

		rv = rv.Elem()
	}

	if !rv.IsValid() {
		return nil, nil
	}

	return normalizeScalar(rv.Interface()), nil
}

func (e *contractEvaluator) normalizeArray(value any) (any, error) {
	value, err := e.normalizeScalar(value)
	if err != nil || value == nil {
		return value, err
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Array && rv.Kind() != reflect.Slice {
		return value, nil
	}

	if !e.charge(rv.Len()) {
		return nil, filter.ErrDataLimitExceeded
	}

	out := make([]any, rv.Len())
	for i := range rv.Len() {
		out[i], err = e.normalizeScalar(rv.Index(i).Interface())
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

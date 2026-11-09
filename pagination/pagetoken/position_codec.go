// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"reflect"
	"time"

	"github.com/shopspring/decimal"
)

// ValueComparator orders normalized scalar values. Implementations must be safe for concurrent use.
type ValueComparator interface {
	Compare(SortValue, SortValue) int
}

// PositionCodec defines normalization, ordering and wire encoding for positions.
// Name must change whenever ordering or encoding changes; engines bind tokens to it.
// Implementations must be safe for concurrent use and preserve values exactly on round trips.
type PositionCodec interface {
	PayloadCodec
	ValueComparator
	Normalize(any, time.Duration) (SortValue, error)
	Name() string
}

// ScalarPositionCodec is the portable scalar preset. Its class order is number,
// string, bool, time; custom codecs may replace both this order and representation.
type ScalarPositionCodec struct{ JSONPayloadCodec }

// Name identifies this encoding and ordering contract.
func (ScalarPositionCodec) Name() string { return "scalar-v2" }

// Compare orders the built-in scalar classes.
func (ScalarPositionCodec) Compare(a, b SortValue) int { return a.Compare(b) }

// Normalize preserves scalar types and truncates timestamps to precision.
// A nonpositive precision is invalid.
func (ScalarPositionCodec) Normalize(v any, precision time.Duration) (SortValue, error) {
	if precision <= 0 {
		return SortValue{}, ErrUnsortableValue
	}

	switch x := v.(type) {
	case time.Time:
		return timeValue(x.UTC().Truncate(precision)), nil
	case string, int64, float64, bool, decimal.Decimal:
		return NewSortValue(v)
	}

	rv := reflect.ValueOf(v)

	hops := 0
	for ; rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface); hops++ {
		if hops == maxSortValueHops || rv.IsNil() {
			return SortValue{}, ErrUnsortableValue
		}

		rv = rv.Elem()
	}

	// Only a followed pointer or interface needs the value re-boxed; boxing a value read directly would allocate a copy.
	if hops > 0 && rv.IsValid() {
		v = rv.Interface()
	}

	if t, ok := v.(time.Time); ok {
		return timeValue(t.UTC().Truncate(precision)), nil
	}

	return NewSortValue(v)
}

// OpaqueSortValue holds a custom codec's immutable scalar. The codec must implement
// its encoding and comparison; the built-in JSON codec does not encode opaque values.
func OpaqueSortValue(v any) SortValue {
	if v == nil {
		return SortValue{}
	}

	return SortValue{ref: v, kind: kindOpaque}
}

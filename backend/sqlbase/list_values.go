// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// sortValue refuses a lossy database boundary: sorting still uses the stored value.
func (l *ListEngine[T]) sortValue(v any) (pagetoken.SortValue, error) {
	if at, ok := v.(time.Time); ok {
		if err := l.alignedTimestamp(at); err != nil {
			return pagetoken.SortValue{}, err
		}
	}

	return l.opts.positionCodec.Normalize(v, l.opts.timestampPrecision)
}

// alignedTimestamp refuses a timestamp finer than the storage precision: its cursor would not round-trip.
func (l *ListEngine[T]) alignedTimestamp(at time.Time) error {
	if !at.Equal(at.Truncate(l.opts.timestampPrecision)) {
		return fmt.Errorf("%w: timestamp is not aligned to %s", pagetoken.ErrUnsortableValue, l.opts.timestampPrecision)
	}

	return nil
}

func sortDestination(kind Kind) any {
	switch kind {
	case Text, UUID, Numeric:
		return new(string)
	case Integer:
		return new(int64)
	case Boolean:
		return new(bool)
	case Float:
		return new(float64)
	case Timestamp:
		return new(time.Time)
	default:
		return new(any)
	}
}

func deref(v any) any {
	switch x := v.(type) {
	case *any:
		return *x
	case *string:
		return *x
	case *int64:
		return *x
	case *bool:
		return *x
	case *float64:
		return *x
	case *time.Time:
		return x.UTC()
	default:
		return v
	}
}

func decodeSortValue(v pagetoken.SortValue, kind Kind) (any, error) {
	value, ok := v.Value()
	if !ok {
		return nil, pagetoken.ErrInvalidToken
	}

	if v.Class() == pagetoken.SortClassNone {
		if valuer, ok := value.(driver.Valuer); ok {
			var err error

			value, err = valuer.Value()
			if err != nil {
				return nil, errors.Join(pagetoken.ErrInvalidToken, err)
			}
		}
	}

	switch kind {
	case Text, UUID:
		if _, ok := value.(string); ok {
			return value, nil
		}
	case Integer:
		if _, ok := value.(int64); ok {
			return value, nil
		}
	case Boolean:
		if _, ok := value.(bool); ok {
			return value, nil
		}
	case Float:
		if _, ok := value.(float64); ok {
			return value, nil
		}
	case Timestamp:
		if value, ok := value.(time.Time); ok {
			return value.UTC(), nil
		}
	case Numeric:
		if _, ok := value.(decimal.Decimal); ok {
			return value, nil
		}
	default:
		// No other kind is a valid cursor value.
	}

	return nil, pagetoken.ErrInvalidToken
}

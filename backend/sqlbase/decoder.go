// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"database/sql/driver"
	"reflect"
)

// Decoder converts SQL driver.Valuer values (including sql.Null[T], pgtype.Numeric
// and pgtype.UUID) without introducing a dependency on a particular SQL driver.
// Its zero value is ready for concurrent use.
type Decoder struct{}

// Decode returns the SQL value or preserves a native value. Invalid nullable
// values become nil; errors from custom valuers are returned unchanged.
//
//nolint:nilnil // nil is a successful SQL NULL conversion.
func (Decoder) Decode(value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil, nil
	}

	if v, ok := value.(driver.Valuer); ok {
		return v.Value()
	}

	return value, nil
}

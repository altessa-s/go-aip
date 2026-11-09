// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package literalvalue

import (
	"reflect"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-atlas-core/encoding/keys"
)

// Immutable reports whether copying v cannot alias caller-mutable state.
func Immutable(v any) bool {
	if v == nil {
		return true
	}

	switch v.(type) {
	case time.Time, decimal.Decimal:
		return true
	}

	return immutableType(reflect.TypeOf(v))
}

func immutableType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		return true
	case reflect.Array:
		return immutableType(t.Elem())
	case reflect.Struct:
		for field := range t.Fields() {
			if !immutableType(field.Type) {
				return false
			}
		}

		return true
	default:
		return false
	}
}

// Canonical encodes an immutable value without invoking user formatting methods.
func Canonical(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case time.Time:
		return "time=" + x.UTC().Format(time.RFC3339Nano)
	case decimal.Decimal:
		return "decimal=" + x.String()
	case []byte:
		return "bytes=" + string(x)
	}

	return encode(reflect.ValueOf(v))
}

func encode(v reflect.Value) string {
	t := v.Type()
	parts := []string{typeKey(t)}

	switch v.Kind() {
	case reflect.Bool:
		parts = append(parts, strconv.FormatBool(v.Bool()))
	case reflect.String:
		parts = append(parts, v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parts = append(parts, strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		parts = append(parts, strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		parts = append(parts, strconv.FormatFloat(v.Float(), 'g', -1, t.Bits()))
	case reflect.Complex64, reflect.Complex128:
		parts = append(parts, strconv.FormatComplex(v.Complex(), 'g', -1, t.Bits()))
	case reflect.Array:
		for i := range v.Len() {
			parts = append(parts, encode(v.Index(i)))
		}
	case reflect.Struct:
		for _, field := range v.Fields() {
			parts = append(parts, encode(field))
		}
	default:
		// Unsupported kinds contribute only their type identity.
	}

	return keys.Compose(parts...)
}

// typeKey preserves full package paths even for empty anonymous aggregates.
func typeKey(t reflect.Type) string {
	if t.Name() != "" {
		return keys.Compose(t.PkgPath(), t.Name())
	}

	switch t.Kind() {
	case reflect.Array:
		return keys.Compose("array", strconv.Itoa(t.Len()), typeKey(t.Elem()))
	case reflect.Struct:
		parts := []string{"struct"}
		for f := range t.Fields() {
			parts = append(parts, f.Name, f.PkgPath, typeKey(f.Type), string(f.Tag), strconv.FormatBool(f.Anonymous))
		}

		return keys.Compose(parts...)
	default:
		return t.String()
	}
}

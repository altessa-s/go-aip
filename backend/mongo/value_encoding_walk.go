// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"reflect"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// Lower bounds of the BSON encoding of a value, in bytes: an element's type and key terminator, a length prefix with a
// terminator (strings, binaries, documents and arrays), the payload of a number.
const (
	elementBytes    = 2
	containerBytes  = 5
	integerBytes    = 4
	floatBytes      = 8
	decimal128Bytes = 16
)

// encodeWalk is the state of one [encodable] walk: the values and estimated bytes left, the bytes the custom encodings returned,
// the struct type described last, the evaluation's budget, the types of the registry that encodes the value and the type counted
// in them last.
type encodeWalk struct {
	budget    filtereval.Budget
	nodes     int
	bytes     int
	hookBytes int
	described reflect.Type
	last      *structDescription
	types     *registryTypes
	noted     reflect.Type
}

// note counts type t of a value walked in the registry's types; the values of a list or a chain share their type, which the walk
// counts once in a row.
func (w *encodeWalk) note(t reflect.Type) {
	if t != w.noted {
		w.noted = t
		w.types.note(t)
	}
}

// describe returns the description of struct type t ([describeStruct]), nil once the budget is exhausted: the struct values of a
// list or a chain share their type, which the walk describes, and charges, once in a row.
func (w *encodeWalk) describe(t reflect.Type) *structDescription {
	if t != w.described {
		w.described, w.last = t, describeStruct(t, nil, w.budget)
	}

	return w.last
}

// take accounts for one more value, of size estimated encoded bytes.
func (w *encodeWalk) take(size int) bool {
	if w.nodes == 0 || size > w.bytes {
		return false
	}

	w.nodes--
	w.bytes -= size

	return w.budget.Charge(1 + size/encodedBytesPerUnit)
}

// value walks rv, reached through nesting levels and then hops pointers or interfaces.
func (w *encodeWalk) value(rv reflect.Value, nesting, hops int) bool {
	if !rv.IsValid() {
		return true
	}

	t := rv.Type()
	w.note(t)

	if ok, handled := w.special(rv, t, nesting, hops); handled {
		return ok
	}

	if recv, iface, ok := encodingHook(rv); ok {
		return w.hook(recv, iface, nesting)
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return w.take(elementBytes)
		}

		return hops < maxPointerHops && w.take(0) && w.value(rv.Elem(), nesting, hops+1)
	case reflect.String:
		return w.take(elementBytes + containerBytes + rv.Len())
	case reflect.Bool:
		return w.take(elementBytes + 1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64:
		return w.take(elementBytes + integerBytes)
	case reflect.Float32, reflect.Float64:
		return w.take(elementBytes + floatBytes)
	case reflect.Struct:
		return w.structValue(rv, t, nesting)
	case reflect.Map:
		return w.mapValue(rv, t, nesting)
	case reflect.Slice, reflect.Array:
		return w.sequence(rv, t, nesting)
	default: // a kind the driver refuses: it fails without walking it
		return w.take(0)
	}
}

// structValue walks the encoded fields of a struct.
func (w *encodeWalk) structValue(rv reflect.Value, t reflect.Type, nesting int) bool {
	if nesting == maxStoredNesting {
		return false
	}

	d := w.describe(t)
	if d == nil || d.opaque || !w.take(elementBytes+containerBytes) {
		return false
	}

	w.types.describe(t, d)

	for _, f := range d.fields {
		if !w.keyBytes(f.keyBytes) || !w.value(rv.Field(f.index), nesting+1, 0) {
			return false
		}
	}

	return true
}

// mapValue walks a map with string or integer keys, which encodes as a document.
func (w *encodeWalk) mapValue(rv reflect.Value, t reflect.Type, nesting int) bool {
	if rv.IsNil() {
		return w.take(elementBytes)
	}

	if nesting == maxStoredNesting || !plainKeyType(t.Key()) || rv.Len() > w.nodes || !w.take(elementBytes+containerBytes) {
		return false
	}

	stringKeys := t.Key().Kind() == reflect.String

	for it := rv.MapRange(); it.Next(); {
		keyBytes := maxIntegerKeyBytes
		if stringKeys {
			keyBytes = it.Key().Len()
		}

		if !w.keyBytes(keyBytes) || !w.value(it.Value(), nesting+1, 0) {
			return false
		}
	}

	return true
}

// sequence walks a slice or an array: a binary when its elements are bytes, an array of its elements otherwise.
func (w *encodeWalk) sequence(rv reflect.Value, t reflect.Type, nesting int) bool {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return w.take(elementBytes)
	}

	if t.Elem() == byteType { // a binary
		return w.take(elementBytes + containerBytes + 1 + rv.Len())
	}

	if nesting == maxStoredNesting || rv.Len() > w.nodes || !w.take(elementBytes+containerBytes) {
		return false
	}

	for i := range rv.Len() {
		if !w.keyBytes(indexKeyBytes(i)) || !w.value(rv.Index(i), nesting+1, 0) {
			return false
		}
	}

	return true
}

// maxIntegerKeyBytes is the longest key an integer map key encodes to: "-9223372036854775808".
const maxIntegerKeyBytes = 20

// plainKeyType reports whether the driver encodes a map key of type t without running the type's code: as a string, or the
// decimal digits of an integer. A key encoding itself as text (bson.KeyMarshaler, encoding.TextMarshaler) runs code whose work
// is proportional to the key's value — a decimal.Decimal key spells out its exponent — so a map keyed by it is opaque.
func plainKeyType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String:
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return !t.Implements(keyMarshalerType) && !t.Implements(textMarshalerType)
	default:
		return false
	}
}

// decimalBase is the radix of an array index key.
const decimalBase = 10

// indexKeyBytes is the length of the key of element i of an encoded array, its decimal digits.
func indexKeyBytes(i int) int {
	n := 1
	for ; i >= decimalBase; i /= decimalBase {
		n++
	}

	return n
}

// keyBytes accounts for the n bytes of a document key.
func (w *encodeWalk) keyBytes(n int) bool {
	if n > w.bytes {
		return false
	}

	w.bytes -= n

	return w.budget.Charge(n / encodedBytesPerUnit)
}

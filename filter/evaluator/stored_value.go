// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"math"
	"reflect"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-atlas-core/types/optional"
)

// This file reads a data value in its stored form, so that two Go values a store serializes identically are evaluated identically:
// a []string and a []any of the same strings are the same array, a map[string]string and a map[string]any the same document. The
// forms a storage encoding defines — a driver's own types, a struct, a value encoding itself — are read through the evaluation's
// [ValueEncoding] ([WithValueEncoding]). A [Reader] reads only the forms storedValue returns; the default evaluator reads them only
// where it documents so ([nonNullStored]).

// Types read by type rather than by kind.
var (
	byteType    = reflect.TypeFor[byte]()
	decimalType = reflect.TypeFor[decimal.Decimal]()
	timeType    = reflect.TypeFor[time.Time]()
)

// storedValue returns v in its stored form, as a [Reader] reads it (the default evaluator through [nonNullStored]):
//
//   - null: nil, a nil pointer, and a nil slice, map or byte slice;
//   - a number: int64, int32, int, uint64, float64 or decimal.Decimal as is; any other integer kind as int64 (an unsigned value
//     above the int64 range as uint64), a float32 or a named float as float64;
//   - a string, a bool: a named string or bool as its base value;
//   - a timestamp: a time.Time;
//   - bytes: a []byte, any slice or array of byte;
//   - an array: a []any as is; any other slice or array as a []any of its elements;
//   - a document: a map[string]any as is; a map with string keys as a map[string]any;
//   - a pointer, or an interface behind one: the value it points to;
//   - a value whose form the evaluation's [ValueEncoding] defines — its own types, a struct, a map not keyed by strings, a value
//     encoding itself — as the encoding stores it ([ValueEncoding.Stored], [ValueEncoding.Encode]); without an encoding such a
//     value is returned unchanged, and a type of a storage driver is read by its kind.
//
// The elements of an array and the values of a document are not converted: the evaluator converts each value it reads. A value
// that cannot be stored is returned unchanged and equals no filter literal: one beyond the bounds of [MaxPointerHops],
// [MaxStoredNesting] and [MaxStoredNodes], a cyclic one included, a typed slice, array or map with more than [MaxStoredNodes]
// elements, a document whose keys total more than [MaxDocumentBytes], and one the encoding cannot store. A conversion is
// charged to b — a copied slice or map by its length, a document key by its bytes — and returns v unchanged once b is exhausted.
// The common forms, a []any and a map[string]any included, return without reflection, allocation or charge.
func storedValue(v any, b *dataBudget) any {
	switch x := v.(type) {
	case nil, string, bool, int64, int32, int, float64, uint64, time.Time, decimal.Decimal:
		return v
	case map[string]any:
		if x == nil {
			return nil
		}

		return v
	case []any:
		if x == nil {
			return nil
		}

		return v
	case []byte:
		if x == nil {
			return nil
		}

		return v
	default:
		return convertStored(v, b)
	}
}

// StoredValue returns v in the stored form in which a [Reader] reads a data value (as the MongoDB profile does), so that code reading
// records beside the Evaluator (an in-memory sort, a projection) reads them as it does: pointers and interfaces followed, a nil
// pointer, slice or map null, an int, int32, int64, uint64 or float64 as it is and any other integer kind an int64 (an unsigned
// value above its range a uint64), a float32 a float64, a named string or bool
// its base value, a slice or array of bytes a []byte, any other slice or array a []any, a map with string keys a map[string]any,
// and a value whose form enc defines — a storage driver's own types, a struct, a value encoding itself — as enc stores it
// ([ValueEncoding]). Without enc, an optional.Optional is its value or null, and any other struct is returned unchanged. The
// elements of a []any and the values of a map[string]any are not converted: call StoredValue on each value read.
//
// The work of a conversion is charged to b, which may be nil for no bound; once b refuses a charge the value is returned unchanged,
// as a value beyond the conversion bounds (a cyclic one included) is. Values already in a stored form return without allocation
// or charge. StoredValue is safe for concurrent use when enc and b are.
func StoredValue(v any, enc ValueEncoding, b Budget) any {
	budget := dataBudget{left: math.MaxInt, enc: enc, ext: b}

	return storedValue(v, &budget)
}

// The default evaluator (without a profile) reads a data value as the Go value it is, as it always has: a path steps only
// through a map[string]any, null is only a nil value, a nil []any, map[string]any or []byte is an empty list, document or byte
// string, `in`, size() and the string functions take a value of another Go type as it is. Only where reading a value of a Go type
// it does not know ([plainType]) as it is fails with ErrUnsupportedType — comparing it with ==, !=, <, <=, > or >= — and on the
// fields declared by [filter.WithArrayFields], whose values it reads as a MongoDB query does, it reads the stored form ([nonNullStored]).

// comparable returns the operand v of a comparison as the default evaluator compares it: as it is when it is of a type the
// evaluator knows ([plainType]), which never fails with ErrUnsupportedType for its type; otherwise, where reading it as it is would,
// in its stored form ([nonNullStored]). nonNullStored never returns nil for a value that is not, so a comparison with null reads
// such a value as it always has.
func (e *evaluation) comparable(v any) any {
	if plainType(v) {
		return v
	}

	return nonNullStored(v, &e.budget)
}

// plainType reports whether v is nil or of a Go type the evaluator has always read as it is: a number of the types a filter literal
// has, a string, a bool, a time.Time, a decimal.Decimal, a []byte, a []any or a map[string]any.
func plainType(v any) bool {
	switch v.(type) {
	case nil, string, bool, int64, int32, int, float64, uint64, time.Time, decimal.Decimal, []byte, []any, map[string]any:
		return true
	default:
		return false
	}
}

// nonNullStored is [storedValue] for the default evaluator, which reads null only for a nil value: a nil slice, array or map of
// any type, which storedValue reads as null, is the empty list, byte string or document a nil []any, []byte or map[string]any is
// (that typed nil itself); a nil pointer, and a value stored as null (a bson.Null, an empty Optional, a custom encoding), is the
// value it is, which no literal equals.
func nonNullStored(v any, b *dataBudget) any {
	if plainType(v) {
		return v
	}

	if s := convertStored(v, b); s != nil {
		return s
	}

	return emptyForm(v, b.enc)
}

// emptyForm returns the empty form of v when v, or the value its pointers lead to, is a nil slice or map: a nil []byte for one of
// bytes, a nil map[string]any for a map or a slice of document elements ([ValueEncoding.DocumentElement]), a nil []any for any
// other slice. It returns v otherwise.
func emptyForm(v any, enc ValueEncoding) any {
	rv := reflect.ValueOf(v)
	for hops := 0; (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) && !rv.IsNil(); hops++ {
		if hops == MaxPointerHops {
			return v
		}

		rv = rv.Elem()
	}

	switch {
	case rv.Kind() == reflect.Slice && rv.IsNil():
		switch elem := rv.Type().Elem(); {
		case elem == byteType:
			return []byte(nil)
		case documentElement(enc, elem):
			return map[string]any(nil)
		default:
			return []any(nil)
		}
	case rv.Kind() == reflect.Map && rv.IsNil():
		return map[string]any(nil)
	default:
		return v
	}
}

// sameValue reports whether a and b, neither of them of a [plainType], are equal as Go's == finds them, which is how the default
// evaluator's `in` has always compared such values (reflect.Value.Comparable, then ==): of the same type, comparable — an interface
// inside included, by the dynamic value it holds — and equal. A value of a type == does not compare, or holding a dynamic value it
// does not (a slice, a map, a func), equals nothing. The comparison is charged to b before == runs: a value of a type that holds
// neither an interface nor a string by its size, any other by every value its walk visits ([comparableWithin]) and the bytes of
// its strings. ok is false once b is exhausted.
func sameValue(a, c any, b *dataBudget) (equal, ok bool) {
	t := reflect.TypeOf(a)
	if t != reflect.TypeOf(c) || !t.Comparable() {
		return false, true
	}

	variable, ok := holdsVariable(t, 0, b)
	if !ok {
		return false, false
	}

	if !variable { // == compares the memory of the value, whatever its length: a [1<<30]struct{} is no work
		if !b.charge(1 + int(t.Size())/BytesPerUnit) {
			return false, false
		}

		return a == c, true
	}

	for _, v := range [...]any{a, c} {
		canCompare, ok := comparableWithin(reflect.ValueOf(v), b)
		if !ok {
			return false, false
		}

		if !canCompare {
			return false, true
		}
	}

	return a == c, true
}

// comparableWithin reports, as reflect.Value.Comparable does, whether == can compare v: every interface inside holds nil or a
// comparable dynamic value. It walks v without recursion, charging b a unit for every value it visits and the bytes of every string
// (which == compares), so it visits nothing it has not charged; an array or a struct that holds neither an interface nor a string
// ([holdsVariable]) is charged by its size and not walked. Pointers are not followed (== compares their addresses), so the walk meets
// every part of v once and cannot cycle. ok is false once b is exhausted.
func comparableWithin(v reflect.Value, b *dataBudget) (canCompare, ok bool) {
	stack := []reflect.Value{v}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if !b.charge(1) {
			return false, false
		}

		switch x.Kind() {
		case reflect.Interface:
			if !x.IsNil() {
				stack = append(stack, x.Elem())
			}
		case reflect.String:
			if !b.charge(x.Len() / BytesPerUnit) {
				return false, false
			}
		case reflect.Array, reflect.Struct:
			var charged bool
			if stack, charged = pushVariableParts(x, b, stack); !charged {
				return false, false
			}
		default:
			if !x.Type().Comparable() {
				return false, true
			}
		}
	}

	return true, true
}

// pushVariableParts is [comparableWithin] for an array or a struct x: one that holds neither an interface nor a string
// ([holdsVariable]) is charged by its size, any other has its elements or fields pushed onto stack, charged before they are pushed.
// It reports false once b is exhausted.
func pushVariableParts(x reflect.Value, b *dataBudget, stack []reflect.Value) ([]reflect.Value, bool) {
	variable, ok := holdsVariable(x.Type(), 0, b)
	if !ok {
		return stack, false
	}

	if !variable {
		return stack, b.charge(int(x.Type().Size()) / BytesPerUnit)
	}

	if x.Kind() == reflect.Array {
		if !b.charge(x.Len()) { // before the elements are pushed
			return stack, false
		}

		for i := range x.Len() {
			stack = append(stack, x.Index(i))
		}

		return stack, true
	}

	if !b.charge(x.NumField()) { // before the fields are pushed
		return stack, false
	}

	for _, field := range x.Fields() {
		stack = append(stack, field)
	}

	return stack, true
}

// holdsVariable reports whether a value of type t holds an interface or a string, whose == depends on what it holds: t is one, or an
// array or a struct with one inside. It charges b a unit for every type it inspects, before inspecting it, so a wide or deep type
// costs what it is inspected for; ok is false once b is exhausted. A type nested deeper than [MaxStoredNesting] is reported as
// variable, so its values are walked, charged as they go, rather than its type recursed further.
func holdsVariable(t reflect.Type, depth int, b *dataBudget) (variable, ok bool) {
	if !b.charge(1) {
		return false, false
	}

	if depth > MaxStoredNesting {
		return true, true
	}

	switch t.Kind() {
	case reflect.Interface, reflect.String:
		return true, true
	case reflect.Array:
		return holdsVariable(t.Elem(), depth+1, b)
	case reflect.Struct:
		for field := range t.Fields() {
			if variable, ok := holdsVariable(field.Type, depth+1, b); variable || !ok {
				return variable, ok
			}
		}

		return false, true
	default:
		return false, true
	}
}

// convertStored is [storedValue] for the forms that need converting: a float32, the forms the evaluation's [ValueEncoding] defines,
// and the others by kind.
func convertStored(v any, b *dataBudget) any {
	if x, ok := v.(float32); ok {
		return float64(x)
	}

	if b.enc != nil {
		if s, handled := b.enc.Stored(v, b); handled {
			return s
		}
	}

	return reflectStored(v, b)
}

// encodeValue returns v — a struct, a map not keyed by strings, a value of a kind the evaluator does not read, a pointer whose
// method set encodes what it points to — as the evaluation's [ValueEncoding] stores it. Without one, an optional.Optional (or a
// pointer to one) is its value, or null when empty, and any other v is returned unchanged.
func encodeValue(v any, b *dataBudget) any {
	if b.enc != nil {
		return b.enc.Encode(v, b)
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}

	if !optional.IsOptionalType(rv.Type()) || !b.charge(1+int(rv.Type().Size())/BytesPerUnit) { // GetReflect may copy it
		return v
	}

	inner, present := optional.GetReflect(rv)
	if !present {
		return nil
	}

	return storedValue(inner.Interface(), b)
}

// reflectStored is [storedValue] by kind, for the types without a fixed form.
func reflectStored(v any, b *dataBudget) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		return pointerStored(v, rv, b)
	}

	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.String:
		return rv.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u := rv.Uint(); u > math.MaxInt64 {
			return u
		}

		return int64(rv.Uint()) //nolint:gosec // checked above
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}

		if rv.Len() > MaxStoredNodes || !b.charge(copyCost(rv)) { // before copying: a [1<<30]struct{} takes no memory, its []any 16 GiB
			return v
		}

		return sequenceValue(rv)
	case reflect.Map:
		return mapStored(v, rv, b)
	default: // a struct, or a kind an encoder refuses
		return encodeValue(v, b)
	}
}

// pointerStored is [reflectStored] of v, a pointer rv: the value it reaches, read as it is stored.
func pointerStored(v any, rv reflect.Value, b *dataBudget) any {
	// Follow pointers and the interfaces they point to (a *any) together: storedValue of the value reached is never a pointer
	// again, so a cycle through an interface cannot recurse.
	for hops := 0; rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface; hops++ {
		switch {
		case rv.IsNil():
			return nil
		case hops == MaxPointerHops:
			return v
		case rv.Kind() == reflect.Pointer && b.enc != nil && b.enc.EncodesItself(rv.Type()):
			return encodeValue(rv.Interface(), b) // a pointer's method set holds the encoding of the value it points to
		}

		rv = rv.Elem()
	}

	if encodedAddressed(rv, b.enc) {
		// An encoder encodes the struct behind a pointer with its fields addressable, so a field's type encodes itself
		// through a method of its pointer too: the struct is encoded from its address, not from a copy.
		return encodeValue(rv.Addr().Interface(), b)
	}
	// Reading the value behind the pointer copies it: an array beyond the elements read is opaque without the copy, and the
	// copy of any other array or struct is charged by its size.
	if rv.Kind() == reflect.Array && rv.Len() > MaxStoredNodes {
		return v
	}

	if (rv.Kind() == reflect.Array || rv.Kind() == reflect.Struct) && !b.charge(int(rv.Type().Size())/BytesPerUnit) {
		return v
	}

	return storedValue(rv.Interface(), b)
}

// mapStored is [reflectStored] of v, a map rv: a document when its keys are strings.
func mapStored(v any, rv reflect.Value, b *dataBudget) any {
	if rv.Type().Key().Kind() != reflect.String {
		return encodeValue(v, b)
	}

	if rv.IsNil() {
		return nil
	}

	if rv.Len() > MaxStoredNodes || !b.charge(copyCost(rv)) {
		return v
	}

	doc := make(map[string]any, rv.Len())
	keys := DocumentKeys{Budget: b}

	for it := rv.MapRange(); it.Next(); {
		key := it.Key().String()
		if !keys.Take(key) { // before hashing it
			return v
		}

		doc[key] = it.Value().Interface()
	}

	return doc
}

// encodedAddressed reports whether rv, reached through a pointer, is a struct storedValue reads through enc ([encodeValue]) rather
// than as a fixed form: neither a time.Time, a decimal.Decimal nor a type of the encoding ([ValueEncoding.Owns]).
func encodedAddressed(rv reflect.Value, enc ValueEncoding) bool {
	if rv.Kind() != reflect.Struct || !rv.CanAddr() {
		return false
	}

	t := rv.Type()

	return t != timeType && t != decimalType && (enc == nil || !enc.Owns(t))
}

// copyCost is the cost of copying the elements of a slice, an array or a map, which have at most [MaxStoredNodes]: a unit per
// element, and one per [BytesPerUnit] bytes of each, which reading an element copies.
func copyCost(rv reflect.Value) int {
	return rv.Len() * (1 + int(rv.Type().Elem().Size())/BytesPerUnit)
}

// sequenceValue reads a slice or an array as it is stored: bytes a binary, anything else an array. The caller has charged its
// length.
func sequenceValue(rv reflect.Value) any {
	if rv.Type().Elem() == byteType {
		data := make([]byte, rv.Len())
		reflect.Copy(reflect.ValueOf(data), rv)

		return data
	}

	list := make([]any, rv.Len())
	for i := range list {
		list[i] = rv.Index(i).Interface()
	}

	return list
}

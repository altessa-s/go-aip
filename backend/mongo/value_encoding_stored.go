// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// valueEncoding reads values through the driver's types and the [NewRegistry] codecs.
type valueEncoding struct{}

// Stored reads the BSON types, the driver's other types (bson.Raw, bson.RawValue, bsoncore.Document, ...), the values encoding
// themselves, a json.Number and a slice or array of bson.E (a document) as the driver stores them.
func (valueEncoding) Stored(v any, b filtereval.Budget) (any, bool) {
	switch x := v.(type) {
	case bson.A:
		if x == nil {
			return nil, true
		}

		return []any(x), true
	case bson.M:
		if x == nil {
			return nil, true
		}

		return map[string]any(x), true
	case bson.D:
		if x == nil {
			return nil, true
		}

		if len(x) > maxStoredNodes || !b.Charge(len(x)) {
			return v, true
		}

		if doc, ok := elementsDocument(len(x), func(i int) (string, any) { return x[i].Key, x[i].Value }, b); ok {
			return doc, true
		}

		return v, true
	case bson.DateTime:
		return x.Time(), true
	case bson.Decimal128:
		return decimal128Value(x), true
	case bson.Binary:
		if x.Subtype != bson.TypeBinaryGeneric {
			return v, true
		}

		if x.Data == nil {
			return []byte{}, true
		}

		return x.Data, true
	case bson.Null:
		return nil, true
	case bson.Symbol, bson.ObjectID, bson.Timestamp, bson.Regex, bson.JavaScript, bson.CodeWithScope, bson.DBPointer, bson.MinKey,
		bson.MaxKey, bson.Undefined:
		return v, true
	case bson.ValueMarshaler, bson.Marshaler, json.Number:
		return driverValue(v, b), true
	}

	t := reflect.TypeOf(v)
	switch {
	case t == nil:
		return nil, false
	case strings.HasPrefix(t.PkgPath(), driverPackage):
		return driverValue(v, b), true // bson.Raw, bson.RawValue, bsoncore.Document, ...: encoded by type
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem() == elementType:
		return elementsValue(v, b), true
	default:
		return nil, false
	}
}

// Encode encodes v through the driver ([driverValue]).
func (valueEncoding) Encode(v any, b filtereval.Budget) any {
	return driverValue(v, b)
}

// EncodesItself reports whether t encodes itself, through bson.ValueMarshaler or bson.Marshaler.
func (valueEncoding) EncodesItself(t reflect.Type) bool {
	return hasHook(t)
}

// Owns reports whether t is a type of the driver, which carries its own BSON encoding.
func (valueEncoding) Owns(t reflect.Type) bool {
	return strings.HasPrefix(t.PkgPath(), driverPackage)
}

// DocumentElement reports whether elem is bson.E, a sequence of which the driver stores as a document.
func (valueEncoding) DocumentElement(elem reflect.Type) bool {
	return elem == elementType
}

// StringValue returns a symbol as its string: MongoDB orders and matches it as a string.
func (valueEncoding) StringValue(v any) (string, bool) {
	s, ok := v.(bson.Symbol)
	return string(s), ok
}

// BinaryValue returns the subtype and data of a bson.Binary, which [valueEncoding.Stored] keeps for a subtype other than the
// generic one.
func (valueEncoding) BinaryValue(v any) (subtype byte, data []byte, ok bool) {
	x, ok := v.(bson.Binary)
	return x.Subtype, x.Data, ok
}

// Equal reports whether a and b are the same value of one of the BSON types [valueEncoding.Stored] keeps, all of them comparable
// with == at a cost fixed by the type or charged by their strings ([valueEncoding.ScanLength]); a bson.Binary compares by subtype
// and content, a bson.CodeWithScope, which holds a document, equals nothing.
func (valueEncoding) Equal(a, b any) bool {
	switch x := a.(type) {
	case bson.Symbol, bson.JavaScript, bson.ObjectID, bson.Timestamp, bson.Regex, bson.DBPointer, bson.MinKey, bson.MaxKey,
		bson.Undefined:
		return a == b
	case bson.Binary:
		y, ok := b.(bson.Binary)
		return ok && x.Subtype == y.Subtype && bytes.Equal(x.Data, y.Data)
	default:
		return false
	}
}

// ScanLength returns the bytes comparing v with a value of its class may scan: the strings of a symbol, JavaScript code, a regular
// expression or a DB pointer, the data of a binary.
func (valueEncoding) ScanLength(v any) (n int, ok bool) {
	switch x := v.(type) {
	case bson.Symbol:
		return len(x), true
	case bson.JavaScript:
		return len(x), true
	case bson.Binary:
		return len(x.Data), true
	case bson.Regex:
		return len(x.Pattern) + len(x.Options), true
	case bson.DBPointer:
		return len(x.DB), true
	default:
		return 0, false
	}
}

// stored returns a value the driver decoded in its stored form: the BSON types as [valueEncoding.Stored] reads them, any other (a
// base value) as it is.
func stored(v any, b filtereval.Budget) any {
	if s, ok := (valueEncoding{}).Stored(v, b); ok {
		return s
	}

	return v
}

// elementsValue reads a slice or an array of bson.E as the document the driver stores: nil for a nil slice, the document of its
// elements, a repeated key keeping its first value; v itself beyond [maxStoredNodes] elements, when its copy exceeds b, or when its
// keys exceed b or [maxDocumentBytes].
func elementsValue(v any, b filtereval.Budget) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}

	if rv.Len() > maxStoredNodes || !b.Charge(copyCost(rv)) { // before copying
		return v
	}

	at := func(i int) (string, any) {
		e := rv.Index(i) // the fields of a bson.E, read without copying it
		return e.Field(0).String(), e.Field(1).Interface()
	}
	if doc, ok := elementsDocument(rv.Len(), at, b); ok {
		return doc
	}

	return v
}

// copyCost is the cost of copying the elements of a slice or an array, which have at most [maxStoredNodes]: a unit per element, and
// one per [bytesPerUnit] bytes of each, which reading an element copies.
func copyCost(rv reflect.Value) int {
	return rv.Len() * (1 + int(rv.Type().Elem().Size())/bytesPerUnit)
}

// elementsDocument returns the document of the n elements of bson.E at returns; a repeated key keeps its first value, the one a
// MongoDB query reads. The caller has charged n; the bytes of every key are charged to b before the key is hashed, and ok is
// false when they exceed b or, together, [maxDocumentBytes]. The document grows with its distinct keys, never to n at once: n
// elements may repeat one key.
func elementsDocument(n int, at func(int) (string, any), b filtereval.Budget) (doc map[string]any, ok bool) {
	doc = make(map[string]any, min(n, elementsHint))
	keys := filtereval.DocumentKeys{Budget: b}

	for i := range n {
		key, value := at(i)
		if !keys.Take(key) {
			return nil, false
		}

		if _, seen := doc[key]; !seen {
			doc[key] = value
		}
	}

	return doc, true
}

// elementsHint bounds the size a document read from a sequence of bson.E is allocated with.
const elementsHint = 64

// decimal128Value returns d as a decimal.Decimal, and NaN and the infinities, which no decimal.Decimal holds, as the float64 of the
// same value: MongoDB compares numbers by value across their types, so a Decimal128 infinity orders beyond every number and equals
// the double one, and a Decimal128 NaN equals NaN and is unordered against any other number.
func decimal128Value(d bson.Decimal128) any {
	if d.IsNaN() {
		return math.NaN()
	}

	if sign := d.IsInf(); sign != 0 {
		return math.Inf(sign)
	}

	coefficient, exp, err := d.BigInt()
	if err != nil {
		return d
	}

	return decimal.NewFromBigInt(coefficient, int32(exp)) //nolint:gosec // a Decimal128 exponent fits an int32
}

// driverValue encodes v with [storedRegistry] and reads the BSON value back, in its stored form ([stored]). A value the driver cannot
// encode, or must not be asked to ([encodable]), whose encoding exceeds [maxDocumentBytes] or nests deeper than
// [maxStoredNesting] (a custom encoding or a bson.Raw may), is returned unchanged; so is any value once b is exhausted. The
// encoding and its decoding are charged to b by their size.
func driverValue(v any, b filtereval.Budget) any {
	// A registry is built only for a value the driver encodes: without one in the pool, the walk counts the types it meets on
	// their own, as a new registry meets them.
	h := acquireHookEncoder()

	var met registryTypes

	types := &met
	if h != nil {
		types = &h.types
	}

	hookBytes, ok := encodable(v, b, types)
	if !ok {
		h.release()
		return v
	}

	if h == nil {
		h = newHookEncoder(met)
	}

	defer h.release()

	raw, err := h.marshal(v, hookBytes)
	if err != nil || len(raw.Value) > maxDocumentBytes || !b.Charge(1+len(raw.Value)/encodedBytesPerUnit) ||
		!nestingWithin(raw.Type, raw.Value) {
		return v
	}

	var out any // decoded by the driver's default registry into its fixed types (bson.D, bson.A, ...), which bounds its caches

	if guarded(func() error { return raw.Unmarshal(&out) }) != nil {
		return v
	}

	return stored(out, b) // the decoded types all have a fixed form, so this does not come back here
}

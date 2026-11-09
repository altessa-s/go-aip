// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"net/url"
	"reflect"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/decimal128"
)

// special walks the types storedRegistry encodes by their type rather than their kind: an optional.Optional or a pointer to one
// (its value, or null), a decimal.Decimal, a vector, a URL user info and the driver's raw BSON. handled reports whether t is one of
// them.
func (w *encodeWalk) special(rv reflect.Value, t reflect.Type, nesting, hops int) (ok, handled bool) {
	switch {
	case optional.IsOptionalType(t): // encoded by storedRegistry as its value, or null
		if !rv.CanAddr() && !w.budget.Charge(int(t.Size())/bytesPerUnit) { // GetReflect copies it
			return false, true
		}

		inner, present := optional.GetReflect(rv)

		return w.take(elementBytes) && (!present || (hops < maxPointerHops && w.value(inner, nesting, hops+1))), true
	case t.Kind() == reflect.Pointer && optional.IsOptionalType(t.Elem()): // so is a pointer to one, rather than through its hook
		if rv.IsNil() {
			return w.take(elementBytes), true
		}

		return hops < maxPointerHops && w.take(0) && w.value(rv.Elem(), nesting, hops+1), true
	case t == decimalType:
		return w.decimal(rv), true
	case t == vectorType:
		return w.vector(rv), true
	case t == userinfoType:
		return w.userinfo(rv), true
	case isRawType(t):
		return w.raw(rv, nesting), true
	default:
		return false, false
	}
}

// decimal accounts for a decimal.Decimal, which storedRegistry encodes as a Decimal128 after copying its coefficient and dividing
// it by ten until it fits: a coefficient beyond [maxCoefficientBits] is refused before the codec copies it, the divisions of one
// within the bound are charged.
func (w *encodeWalk) decimal(rv reflect.Value) bool {
	if !rv.CanInterface() {
		return false
	}

	d, ok := reflect.TypeAssert[decimal.Decimal](rv)
	if !ok {
		return false
	}

	cost := decimalCompareCost

	if !decimal128.SmallCoefficient(d) {
		if !decimal128.CoefficientWithinLimit(d) { // before the codec copies it
			return false
		}

		cost = largeDecimalEncodeCost
	}

	return w.take(elementBytes+decimal128Bytes) && w.budget.Charge(cost)
}

// Bytes of a BSON vector besides its elements: the binary subtype, and the vector's type and padding.
const vectorHeaderBytes = 3

// vector accounts for a bson.Vector, which the driver encodes as a binary of its elements.
func (w *encodeWalk) vector(rv reflect.Value) bool {
	if !rv.CanInterface() {
		return false
	}

	v, ok := reflect.TypeAssert[bson.Vector](rv)
	if !ok {
		return false
	}

	var size int
	if data, isInt8 := v.Int8OK(); isInt8 {
		size = len(data)
	} else if data, isFloat32 := v.Float32OK(); isFloat32 {
		size = len(data) * float32Bytes
	} else if data, _, isBits := v.PackedBitOK(); isBits {
		size = len(data)
	}

	return w.take(elementBytes + containerBytes + vectorHeaderBytes + size)
}

// float32Bytes is the size of a float32 element of a vector.
const float32Bytes = 4

// userinfo accounts for the user of a url.URL, which the driver encodes as its string, the user's name and password included.
func (w *encodeWalk) userinfo(rv reflect.Value) bool {
	if !rv.CanInterface() {
		return false
	}

	user, ok := reflect.TypeAssert[*url.Userinfo](rv)
	if !ok {
		return false
	}

	password, _ := user.Password() // nil-safe, as Username is

	return w.take(len(user.Username()) + len(password))
}

// largeDecimalEncodeCost is the encoding of a decimal whose coefficient a Decimal128 cannot hold as is: the codec divides it by ten
// once per trailing zero, at most once per digit of a [maxCoefficientBits]-bit coefficient, each division reading its bytes.
const largeDecimalEncodeCost = maxCoefficientDigits * (maxCoefficientBits / bitsPerByte) / bytesPerUnit

// bitsPerByte converts a size in bits to bytes.
const bitsPerByte = 8

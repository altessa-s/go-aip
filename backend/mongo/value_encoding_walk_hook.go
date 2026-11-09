// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"reflect"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonraw"
)

// encodingHook returns the receiver and the interface through which the driver encodes rv when its type encodes itself, looking
// them up as the driver does: the first of [hookInterfaces] the type implements, or its pointer does when rv is addressable. An
// interface value is encoded by the value it holds.
func encodingHook(rv reflect.Value) (recv reflect.Value, iface reflect.Type, ok bool) {
	t := rv.Type()
	if t.Kind() == reflect.Interface {
		return reflect.Value{}, nil, false
	}

	for _, iface := range hookInterfaces {
		if t.Implements(iface) {
			return rv, iface, true
		}

		if t.Kind() != reflect.Pointer && rv.CanAddr() && reflect.PointerTo(t).Implements(iface) {
			return rv.Addr(), iface, true
		}
	}

	return reflect.Value{}, nil, false
}

// hook accounts for a value that encodes itself, reached through nesting levels, by the size of what its hook returns, which the
// driver copies into the encoding whole: the hook is called here, and again by the driver once the whole value is found within the
// bounds, which may then write no more than the hooks returned here ([hookEncoder]). What the hook returns must be well formed raw
// BSON nesting within [maxStoredNesting] levels in all ([bsonraw.Walk]), which the driver reads trusting its lengths. A nil pointer
// whose pointee encodes itself is null, as the driver writes it. What the hook does to produce its encoding is the type's own code,
// not charged; what it returns is.
func (w *encodeWalk) hook(recv reflect.Value, iface reflect.Type, nesting int) bool {
	if recv.Kind() == reflect.Pointer && recv.IsNil() && pointee(recv.Type()).Implements(iface) {
		return w.take(elementBytes)
	}

	if !recv.CanInterface() || (recv.Kind() != reflect.Pointer && !w.budget.Charge(int(recv.Type().Size())/bytesPerUnit)) {
		return false // Interface copies a value that is not a pointer
	}

	t, data, err := callHook(recv, iface)
	if err != nil || !w.take(elementBytes+len(data)) || bsonraw.Walk(t, data, maxStoredNesting-nesting, nil) != nil {
		return false
	}

	w.hookBytes += len(data)

	return true
}

// pointee returns the type t points to through all its pointers.
func pointee(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// hasHook reports whether t encodes itself, through bson.ValueMarshaler or bson.Marshaler.
func hasHook(t reflect.Type) bool {
	return t.Implements(valueMarshalerType) || t.Implements(marshalerType)
}

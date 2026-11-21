// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"errors"
	"reflect"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"

	"github.com/altessa-s/go-mongox/bsonraw"
)

// This file guards the driver against raw BSON: the driver copies the raw BSON of bson.Raw, bson.RawValue, bsoncore.Document,
// bsoncore.Array and of the output of a value encoding itself trusting the lengths inside, and a length below its own header makes
// it slice out of range and panic. Raw BSON is checked ([bsonraw.Walk]) before any encoder of the driver reads it, and the
// driver's encoding and decoding run behind a recover boundary besides ([guarded]).

// nestingWithin reports whether the BSON value of type t encoded as data is well formed ([bsonraw.Walk]) and nests its documents
// and arrays at most [maxStoredNesting] levels, so that decoding it, which the driver does recursively, is bounded. It reads data
// once, without recursion.
func nestingWithin(t bson.Type, data []byte) bool {
	return bsonraw.Walk(t, data, maxStoredNesting, nil) == nil
}

// The types of the driver holding raw BSON that its encoders copy by reading it: bson.Raw and bsoncore.Document as a document,
// bsoncore.Array as an array, bson.RawValue as a value of its type. A bson.RawArray has no encoder of its own and is written as a
// binary, without being read.
var (
	rawType          = reflect.TypeFor[bson.Raw]()
	coreDocumentType = reflect.TypeFor[bsoncore.Document]()
	coreArrayType    = reflect.TypeFor[bsoncore.Array]()
)

// isRawType reports whether t holds raw BSON the driver's encoders read ([rawBSON]).
func isRawType(t reflect.Type) bool {
	return t == rawType || t == coreDocumentType || t == coreArrayType || t == rawValueType
}

// rawBSON returns the BSON type and the bytes of rv, a value of a type [isRawType] reports; ok is false when they cannot be read.
func rawBSON(rv reflect.Value) (t bson.Type, data []byte, ok bool) {
	if !rv.CanInterface() {
		return 0, nil, false
	}

	switch x := rv.Interface().(type) {
	case bson.Raw:
		return bson.TypeEmbeddedDocument, x, true
	case bsoncore.Document:
		return bson.TypeEmbeddedDocument, x, true
	case bsoncore.Array:
		return bson.TypeArray, x, true
	case bson.RawValue:
		return x.Type, x.Value, true
	default:
		return 0, nil, false
	}
}

// raw accounts for raw BSON, reached through nesting levels, by its size, and checks it is well formed and nests within
// [maxStoredNesting] levels in all before the driver reads it.
func (w *encodeWalk) raw(rv reflect.Value, nesting int) bool {
	t, data, ok := rawBSON(rv)

	return ok && w.take(elementBytes+len(data)) && bsonraw.Walk(t, data, maxStoredNesting-nesting, nil) == nil
}

// errMalformedHookOutput fails an encoding in which a value encoding itself returns raw BSON that is not well formed.
var errMalformedHookOutput = errors.New("a value encodes itself to malformed BSON")

// errDriverPanic fails an encoding or a decoding in which the driver panicked.
var errDriverPanic = errors.New("the BSON driver panicked")

// guarded runs f, an encoding or a decoding by the driver, and returns [errDriverPanic] when it panics. The raw BSON the driver
// reads is checked before, but an application registry may encode a value through code that is not looked at — a hook it calls
// again, which may return something else the second time — so a panic of the driver makes the value opaque instead of crashing
// the evaluation.
func guarded(f func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errDriverPanic
		}
	}()

	return f()
}

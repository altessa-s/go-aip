// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"encoding"
	"net/url"
	"reflect"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// driverPackage prefixes the import paths of the MongoDB driver, whose types carry their own BSON encodings.
const driverPackage = "go.mongodb.org/mongo-driver/"

// storedRegistry encodes the values valueEncoding reads as [NewRegistry] stores them: a
// decimal.Decimal as a Decimal128, an Optional as its value or null, besides the driver's defaults. It encodes no value: the
// encoder of an Optional is looked up in it once ([optionalEncoder]), so its caches hold a fixed set of types; the registries that
// encode are [hookEncoders].
var storedRegistry = NewRegistry()

// Types the driver encodes by type rather than by kind.
var (
	documentType = reflect.TypeFor[bson.D]()
	elementType  = reflect.TypeFor[bson.E]()
	byteType     = reflect.TypeFor[byte]()
	rawValueType = reflect.TypeFor[bson.RawValue]()
)

// Types the driver encodes by type from state their exported fields do not show: a vector from its elements, a URL with its
// user's name and password, which encodeWalk measures through their methods.
var (
	vectorType   = reflect.TypeFor[bson.Vector]()
	userinfoType = reflect.TypeFor[*url.Userinfo]()
)

// decimalType is encoded by storedRegistry as a Decimal128, which encodeWalk measures from its coefficient.
var decimalType = reflect.TypeFor[decimal.Decimal]()

// The interfaces through which a type encodes itself, hookInterfaces in the order the driver looks them up, and through which a
// map key encodes itself as text.
var (
	marshalerType      = reflect.TypeFor[bson.Marshaler]()
	valueMarshalerType = reflect.TypeFor[bson.ValueMarshaler]()
	hookInterfaces     = [...]reflect.Type{valueMarshalerType, marshalerType}
	keyMarshalerType   = reflect.TypeFor[bson.KeyMarshaler]()
	textMarshalerType  = reflect.TypeFor[encoding.TextMarshaler]()
)

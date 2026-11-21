// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-mongox/bsoncodec"
)

// NewRegistry returns a private BSON registry with the driver's defaults and the codecs of the values go-aip and go-atlas-core
// define: every optional.Optional[T] (its value, or null), decimal.Decimal and decimal.NullDecimal (exact Decimal128) and the
// go-atlas-core civil.Date (a "YYYY-MM-DD" string). Install it on the client (options.Client().SetRegistry) so documents are
// stored the way [ValueEncoding] reads them and the list engine binds its page tokens to ([WithListRegistry]). Configure
// additional codecs ([RegisterDateType]) before using the registry concurrently. Inner values use this registry, including
// user-defined codecs.
func NewRegistry() *bson.Registry {
	return bsoncodec.NewRegistry()
}

// ErrInvalidDate is returned when a date that is not a valid calendar date
// in years 0–9999 is encoded, or a stored string is not a YYYY-MM-DD date.
var ErrInvalidDate = bsoncodec.ErrInvalidDate

// ErrNotADateType is returned by [RegisterDateType] for a type without the
// calendar-date fields.
var ErrNotADateType = bsoncodec.ErrNotADateType

// ErrDecimalOutOfRange is returned when a decimal.Decimal cannot be stored
// exactly as a BSON Decimal128: more than 34 significant digits, or an
// exponent outside [-6176, 6111]. The value is never rounded. A coefficient
// of more than 4096 bits fails before its trailing zeros are folded, in time
// independent of its size. The codec stores exactly the decimals that
// go-aip/decimal128.Fits accepts, in the form decimal128.Canonical returns;
// validate input with decimal128.Fits to refuse a value before it reaches the
// store.
var ErrDecimalOutOfRange = bsoncodec.ErrDecimalOutOfRange

// ErrDecimalNotFinite is returned when a stored Decimal128 is NaN or an
// infinity, which decimal.Decimal cannot hold.
var ErrDecimalNotFinite = bsoncodec.ErrDecimalNotFinite

// RegisterDateType registers on reg the calendar-date codec for T: a struct
// with the fields Year int, Month time.Month and Day int, such as
// cloud.google.com/go/civil.Date, the go-atlas-core civil.Date (registered by
// [NewRegistry]) or a service's own date type. A date is stored as its "YYYY-MM-DD" string —
// nothing can shift it across a day boundary, and the strings sort in date
// order. The zero date is stored as null, or as "" with
// [WithDateZeroAsEmptyString]; both decode to the zero date. An invalid date
// or a year outside 0–9999 fails with [ErrInvalidDate], as does one that T
// rejects through a Valid() bool method; a BSON date is
// rejected, since reading an instant as a calendar date would pick a zone.
// Pointers to T are handled by the driver's pointer codec (nil <-> null).
// A list engine using reg admits T, and optional.Optional[T], as a scope or
// filter value. Register before the registry is used concurrently.
func RegisterDateType[T any](reg *bson.Registry, opts ...DateOption) error {
	if newDateOptions(opts...).zeroAsEmptyString {
		return bsoncodec.RegisterDateType[T](reg, bsoncodec.WithDateZeroAsEmptyString())
	}

	return bsoncodec.RegisterDateType[T](reg)
}

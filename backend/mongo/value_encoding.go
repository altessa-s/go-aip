// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"bytes"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonbound"
	"github.com/altessa-s/go-aip/decimal128"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// This file is the MongoDB [filtereval.ValueEncoding]: it reads a data value the way the MongoDB driver stores it (with the
// [NewRegistry] codecs), so that two Go values the driver serializes identically are evaluated identically: a bson.M and a
// map[string]any are the same document, a bson.D one too, a struct the document the driver encodes it as.

// Bounds and costs shared with the evaluator ([filtereval.MaxStoredNesting], [filtereval.BytesPerUnit], ...) and the coefficient
// bounds of decimals ([decimal128.MaxCoefficientBits]).
const (
	maxPointerHops       = filtereval.MaxPointerHops
	maxStoredNesting     = filtereval.MaxStoredNesting
	maxStoredNodes       = filtereval.MaxStoredNodes
	maxDocumentBytes     = filtereval.MaxDocumentBytes
	bytesPerUnit         = filtereval.BytesPerUnit
	encodedBytesPerUnit  = filtereval.EncodedBytesPerUnit
	decimalCompareCost   = filtereval.DecimalCompareCost
	maxCoefficientBits   = decimal128.MaxCoefficientBits
	maxCoefficientDigits = decimal128.MaxCoefficientDigits
)

// maxWriterNesting bounds the documents the bounded writer of a custom registry may hold open at once: MongoDB's nesting limit,
// the wrapping document and one level of slack for nestingWithin.
const maxWriterNesting = maxStoredNesting + 2

// ValueEncoding returns the [filtereval.ValueEncoding] of the MongoDB driver with the [NewRegistry] codecs: under
// evaluator.WithValueEncoding(mongo.ValueEncoding()) an [filtereval.Evaluator] (or memory.WithListValueEncoding a memory list)
// reads a data value exactly as the driver stores it in MongoDB — bson.M, bson.D, a struct and a value with a custom BSON encoding
// (bson.Marshaler, bson.ValueMarshaler, an optional.Optional) as the document or value the driver encodes, bson.A as a list,
// bson.DateTime as a timestamp, bson.Decimal128 as a decimal (its NaN and infinities the float64 ones), a bson.Binary of the
// generic subtype as bytes, bson.Null as null, a json.Number as the number the driver writes — within the bounds of a value
// MongoDB can store. A Symbol compares as a string and matches the string functions under evaluator.WithProfile, as MongoDB
// treats it; the other BSON types (ObjectID, Timestamp, Regex, JavaScript, ...) are kept and equal only themselves.
// [WithValueEncodingRegistry] encodes through the application's own registry instead. It is safe for concurrent use.
func ValueEncoding(opts ...ValueEncodingOption) *BSONEncoding {
	return &BSONEncoding{opts: *newValueEncodingOptions(opts...)}
}

// BSONEncoding is the [filtereval.ValueEncoding] [ValueEncoding] returns. Besides the interface it implements the evaluator's
// optional extensions (filtereval.DocumentElements, StringExtractor, BinaryExtractor, ScanSizer and Equal) for the BSON types
// it keeps.
type BSONEncoding struct {
	valueEncoding

	opts valueEncodingOptions
}

// Stored reads a value with the registry of [WithValueEncodingRegistry] when one is configured, through the driver's types
// otherwise. A value already in a stored form is never encoded again, so a value Stored or [BSONEncoding.Encode] returned reads
// the same however many times it is read: a base form (a string, a number, a time.Time, a []any, a map[string]any, ...) is not
// handled, as [filtereval.ValueEncoding] specifies, and a value of a BSON type the driver keeps as it is (an ObjectID, a Symbol, a
// Binary of another subtype, ... — [keptBSON]) is its own stored form. The registry encodes every other value.
func (e *BSONEncoding) Stored(v any, b filtereval.Budget) (any, bool) {
	switch {
	case e.opts.registry == nil:
		return e.valueEncoding.Stored(v, b)
	case baseForm(v):
		return v, false
	case keptBSON(v):
		return v, true
	default:
		return e.Encode(v, b), true
	}
}

// baseForm reports whether v is in a base form the evaluator reads by itself ([filtereval.ValueEncoding]).
func baseForm(v any) bool {
	switch v.(type) {
	case nil, string, bool, int64, int32, int, uint64, float64, float32, time.Time, decimal.Decimal, []byte, []any, map[string]any:
		return true
	default:
		return false
	}
}

// keptBSON reports whether v is a value of a BSON type [valueEncoding.Stored] keeps as it is, which is its own stored form: the
// registry of [WithValueEncodingRegistry] does not encode it again, whatever its encoder for the type does.
func keptBSON(v any) bool {
	switch x := v.(type) {
	case bson.Symbol, bson.ObjectID, bson.Timestamp, bson.Regex, bson.JavaScript, bson.CodeWithScope, bson.DBPointer, bson.MinKey,
		bson.MaxKey, bson.Undefined:
		return true
	case bson.Binary:
		return x.Subtype != bson.TypeBinaryGeneric
	default:
		return false
	}
}

// Encode encodes a value through the configured registry, or [NewRegistry]. A value the configured registry encodes is returned
// in its stored form throughout ([storedTree]): base forms and values of the BSON types the driver keeps, which
// [BSONEncoding.Stored] reads as they are, so the evaluator never passes them through the registry again, which would apply an
// encoding that is not idempotent twice.
func (e *BSONEncoding) Encode(v any, b filtereval.Budget) any {
	if e.opts.registry == nil {
		return e.valueEncoding.Encode(v, b)
	}

	if !b.Charge(1) {
		return v
	}

	if _, ok := encodable(v, b, &registryTypes{}); !ok {
		return v
	}

	if !b.Charge(1) {
		return v
	}

	// The registry's encoders write through a bounded writer, which measures and charges every value before the driver's
	// writer buffers it ([bsonbound]).
	var buf bytes.Buffer

	vw, bound := bsonbound.NewValueWriter(bson.NewDocumentWriter(&buf), e.opts.maxDocumentBytes, maxWriterNesting, b)
	enc := bson.NewEncoder(vw)
	enc.SetRegistry(e.opts.registry)

	var out any

	// The application's registry may encode a value through code encodable has not looked at, a hook it calls again included:
	// a panic of the driver it leads to makes the value opaque ([guarded]).
	err := guarded(func() error {
		if err := enc.Encode(bson.D{{Key: "v", Value: v}}); err != nil || bound.Depth() != 0 || buf.Len() > e.opts.maxDocumentBytes {
			return errNotEncoded
		}

		raw, err := bson.Raw(buf.Bytes()).LookupErr("v")
		if err != nil || !nestingWithin(raw.Type, raw.Value) {
			return errNotEncoded
		}

		return raw.Unmarshal(&out)
	})
	if err != nil {
		return v
	}

	s, ok := storedTree(out, b)
	if !ok {
		return v
	}

	return s
}

// errNotEncoded fails an encoding through an application registry that is not within the bounds of a value MongoDB stores.
var errNotEncoded = errors.New("the value is not encoded within the bounds")

// storedTree returns v, a value the driver decoded into its fixed types (bson.D, bson.A, bson.DateTime, ...), in its stored form
// throughout: a document a map[string]any and an array a []any of the stored forms of their values, a value of another BSON type
// [stored] — a base form or a value of a BSON type the driver keeps ([keptBSON]). Every document and array is charged to b by its
// length and every key by its bytes ([elementsDocument]); ok is false beyond [maxStoredNodes] elements or once b is exhausted. The
// caller has bounded the nesting of v ([nestingWithin]).
func storedTree(v any, b filtereval.Budget) (s any, ok bool) {
	switch x := v.(type) {
	case bson.D:
		if x == nil {
			return nil, true
		}

		if len(x) > maxStoredNodes || !b.Charge(len(x)) {
			return nil, false
		}

		var doc map[string]any
		if doc, ok = elementsDocument(len(x), func(i int) (string, any) { return x[i].Key, x[i].Value }, b); !ok {
			return nil, false
		}

		for key, value := range doc {
			if doc[key], ok = storedTree(value, b); !ok {
				return nil, false
			}
		}

		return doc, true
	case bson.A:
		if x == nil {
			return nil, true
		}

		if len(x) > maxStoredNodes || !b.Charge(len(x)) {
			return nil, false
		}

		list := make([]any, len(x))
		for i, value := range x {
			if list[i], ok = storedTree(value, b); !ok {
				return nil, false
			}
		}

		return list, true
	}

	return stored(v, b), true
}

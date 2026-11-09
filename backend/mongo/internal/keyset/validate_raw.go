// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonraw"
)

// validateRawFilterDocument checks an encoded filter as [ValidateFilter] checks raw BSON, without a context.
func validateRawFilterDocument(raw bson.Raw) error {
	w := filterWalk{left: maxFilterNodes, bytesLeft: maxFilterBytes, untilCheck: cancelCheckInterval}

	return w.rawDocument(raw, 0)
}

// rawDocument checks the document or array doc, reached at depth, element by element ([bsonraw.Walk]), charging each to the walk:
// the size is bounded before anything is read, the structure is checked as it is read, every key is checked against the
// deny-list and every type against those [ValidateFilter] admits.
func (w *filterWalk) rawDocument(doc []byte, depth int) error {
	return w.rawValue(bson.TypeEmbeddedDocument, doc, depth)
}

// rawValue checks a raw value of type t, reached at depth: a document or an array as [filterWalk.rawDocument] does, any other value
// must be a whole value of an admitted BSON type. The size is bounded before anything is read.
func (w *filterWalk) rawValue(t bson.Type, data []byte, depth int) error {
	switch {
	case depth > maxFilterNesting:
		return errFilterTooDeep
	case len(data) > maxFilterBytes:
		return errFilterTooLarge
	}

	if err := admitRawType(t); err != nil {
		return err
	}

	// The document reached at depth is open with the documents inside it, up to maxFilterNesting in all.
	err := bsonraw.Walk(t, data, maxFilterNesting-depth+1, func(_ int, et bson.Type, k []byte) error {
		if err := w.take(1); err != nil {
			return err
		}

		if err := key(string(k)); err != nil {
			return err
		}

		return admitRawType(et)
	})

	switch {
	case errors.Is(err, bsonraw.ErrMalformed):
		return errMalformedRaw
	case errors.Is(err, bsonraw.ErrTooDeep):
		return errFilterTooDeep
	default:
		return err
	}
}

// admitRawType fails with [ErrValueType] for a raw BSON type a filter must not hold: server-side code (JavaScript, with or without
// a scope), the deprecated symbol and DBPointer types and unknown types.
func admitRawType(t bson.Type) error {
	switch t { //nolint:exhaustive // the remaining types are refused below
	case bson.TypeNull, bson.TypeUndefined, bson.TypeMinKey, bson.TypeMaxKey, bson.TypeBoolean, bson.TypeInt32, bson.TypeDouble,
		bson.TypeInt64, bson.TypeDateTime, bson.TypeTimestamp, bson.TypeObjectID, bson.TypeDecimal128, bson.TypeString,
		bson.TypeBinary, bson.TypeEmbeddedDocument, bson.TypeArray, bson.TypeRegex:
		return nil
	default:
		return fmt.Errorf("%w: raw BSON type %s", ErrValueType, t)
	}
}

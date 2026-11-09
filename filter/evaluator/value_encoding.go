// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import "reflect"

// ValueEncoding is the storage encoding an [Evaluator] reads data values through ([WithValueEncoding]): the stored form of the
// values whose form the encoding, not their Go kind, defines — a storage driver's own types, a struct or a value encoding itself as
// the encoder writes it — and the comparison of the scalar types it keeps. The evaluator reads every other value by its kind
// itself: base values, pointers, named types, typed slices, arrays and maps with string keys. Without an encoding, a value the
// evaluator cannot read by its kind (a struct, a map not keyed by strings, a func) is opaque: it equals no filter literal and
// paths do not step through it, and a driver's type is read by its Go kind.
//
// backend/mongo provides the encoding of the MongoDB driver (mongo.ValueEncoding, over the mongo.NewRegistry codecs), which makes
// the Evaluator read values exactly as the driver stores them in MongoDB. An implementation is
// called during an evaluation only; it must be safe for concurrent use, charge the work it does to the [Budget] it is given, and
// return the value unchanged once the budget is exhausted.
type ValueEncoding interface {
	// Stored returns v, a value that is not in a base form (string, bool, int64, int32, int, uint64, float64, float32, time.Time,
	// decimal.Decimal, []byte, []any, map[string]any), in its stored form when the encoding defines it: handled is false for a
	// value the evaluator reads by its kind. A value the encoding defines but cannot store is returned unchanged (opaque).
	Stored(v any, budget Budget) (stored any, handled bool)
	// Encode returns v — a struct, a map not keyed by strings, a value of a kind the evaluator does not read, a pointer whose
	// method set encodes the value it points to ([ValueEncoding.EncodesItself]) — as the encoding stores it, or v unchanged when
	// it cannot.
	Encode(v any, budget Budget) any
	// EncodesItself reports whether the values of pointer type t encode themselves, so that the pointer, not the value it points
	// to, is encoded.
	EncodesItself(t reflect.Type) bool
	// Owns reports whether t is a type of the encoding, whose values [ValueEncoding.Stored] reads; a struct of another type
	// reached through a pointer is encoded from its address.
	Owns(t reflect.Type) bool
}

// DocumentElements recognizes document element slices in an encoding.
type DocumentElements interface{ DocumentElement(reflect.Type) bool }

// StringExtractor recognizes driver string types.
type StringExtractor interface{ StringValue(any) (string, bool) }

// BinaryExtractor recognizes binary values with backend subtypes.
type BinaryExtractor interface {
	BinaryValue(any) (byte, []byte, bool)
}

// ScalarEquality compares opaque stored values.
type ScalarEquality interface{ Equal(any, any) bool }

// ScanSizer accounts for bytes scanned when comparing opaque values.
type ScanSizer interface{ ScanLength(any) (int, bool) }

func documentElement(enc ValueEncoding, elem reflect.Type) bool {
	ext, ok := enc.(DocumentElements)
	return ok && ext.DocumentElement(elem)
}

// Budget is the data budget of one evaluation ([WithMaxDataBudget]), which a [ValueEncoding] charges for the work it does: Charge
// spends units, or exhausts the budget and reports false when fewer are left.
type Budget interface {
	Charge(units int) bool
}

// Bounds of a value converted to its stored form ([StoredValue]: a typed slice, a struct, a custom encoding, ...), which the
// evaluator and a [ValueEncoding] share: beyond them the value is opaque — returned unchanged, it matches nothing but != — instead
// of overflowing the stack, which an encoder does on a cyclic value, or exhausting memory. The work of reading any value is charged
// to the evaluation's [Budget] besides.
const (
	// MaxPointerHops bounds the consecutive pointers and interfaces followed, so a self-referencing pointer cannot loop.
	MaxPointerHops = 32
	// MaxStoredNesting is MongoDB's limit on nested documents and arrays: a deeper value cannot be stored, and a cyclic one is
	// infinitely deep.
	MaxStoredNesting = 100
	// MaxStoredNodes bounds the values inspected in one value an encoding encodes, which keeps a value sharing its parts (a pointer
	// DAG whose encoding is exponential in its size) from blowing up, and the elements of a typed slice, array or map copied.
	MaxStoredNodes = 1 << 20
	// MaxDocumentBytes is MongoDB's limit on a document: a value whose encoding is estimated, or found, to be larger cannot be
	// stored, and the keys of one document may total at most as many bytes ([DocumentKeys]).
	MaxDocumentBytes = 16 << 20
)

// DocumentKeys accounts for the keys of a document a conversion to the stored form hashes: each is charged to Budget, a unit per
// [BytesPerUnit] bytes, and all of them together may hold at most [MaxDocumentBytes], beyond which the document cannot be stored.
// The zero value with a Budget set is ready to use.
type DocumentKeys struct {
	// Budget is charged for every key taken; it must not be nil.
	Budget Budget

	bytes int
}

// Take accounts for key, before it is hashed: it reports false once the keys taken exceed [MaxDocumentBytes] or Budget is
// exhausted.
func (k *DocumentKeys) Take(key string) bool {
	k.bytes += len(key)
	return k.bytes <= MaxDocumentBytes && k.Budget.Charge(len(key)/BytesPerUnit)
}

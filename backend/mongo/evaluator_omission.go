// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"reflect"
	"strings"
)

// omittedAt reports whether the stored document lacks the mapped path because the value at it, or at a document on the way to it,
// belongs to a field declared by [WithSchemaOmitEmpty] and is empty: the driver leaves out the whole field, descendants included.
func (e *evaluation) omittedAt(path string) bool {
	if !e.schema.hasOmitEmpty() {
		return false
	}

	for end := 0; ; {
		dot := strings.IndexByte(path[end:], '.')

		prefix := path
		if dot >= 0 {
			prefix = path[:end+dot]
		}

		if e.schema.omitsEmptyAt(prefix) && e.emptyAt(prefix) {
			return true
		}

		if dot < 0 {
			return false
		}

		end += dot + 1
	}
}

// emptyAt reports whether the value at path is empty as the BSON omitempty tag reads it ([WithSchemaOmitEmpty]), judged on the value as
// the data holds it. The path is followed through map[string]any documents only, whose values are the caller's raw Go values. Any
// other document on the way — a struct, a typed map, a bson.D, a pointer to a map — is one the driver encodes itself, applying its
// own omitempty tags, so its values are read as stored and never judged again: the leaf is not empty. So is a path that does not
// lead to a value.
func (e *evaluation) emptyAt(path string) bool {
	var cur any = e.Data()
	for {
		if !e.Charge(1) {
			return false
		}

		doc, ok := cur.(map[string]any)
		if !ok {
			return false
		}

		head, rest, more := strings.Cut(path, ".")

		v, ok := doc[head]
		if !ok {
			return false
		}

		if !more {
			return e.omitEmptyValue(v)
		}

		cur, path = v, rest
	}
}

// omitEmptyValue reports whether the MongoDB driver leaves v out of a document under the omitempty tag, by the rules of the pinned
// driver's struct codec (bson isEmpty) with its default options: a value implementing bson.Zeroer (IsZero() bool) — a non-nil
// pointer included, so a pointer to a zero time.Time or a zero decimal.Decimal — is empty when its IsZero says so; otherwise an
// array, map, slice or string is empty when its length is 0 (an array of a nonzero length never is), a struct is never empty
// (omitempty does not omit a zero struct without the OmitZeroStruct encoder option), and any other value — nil, a nil pointer, a
// zero number or bool — when it is the zero value of its type. Every test takes constant work and is charged one unit of the data
// budget; it reports false when the budget runs out.
func (e *evaluation) omitEmptyValue(v any) bool {
	if !e.Charge(1) {
		return false
	}

	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)

	kind := rv.Kind()
	if (kind != reflect.Pointer || !rv.IsNil()) && rv.Type().Implements(zeroerType) {
		z, _ := reflect.TypeAssert[zeroer](rv) //nolint:errcheck // Implements checked above
		return z.IsZero()
	}

	switch kind {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	case reflect.Struct:
		return false
	default:
		return rv.IsZero()
	}
}

// zeroerType is the interface through which the driver asks a value whether omitempty leaves it out.
var zeroerType = reflect.TypeFor[zeroer]()

// zeroer is the interface through which the MongoDB driver asks a value whether omitempty leaves it out (bson.Zeroer).
type zeroer interface {
	IsZero() bool
}

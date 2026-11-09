// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

var (
	timeType    = reflect.TypeFor[time.Time]()
	decimalType = reflect.TypeFor[decimal.Decimal]()
)

// Reader resolves dotted paths through Go maps, pointers and optional storage
// encodings. It does not choose predicate semantics or a database profile.
type Reader struct {
	encoding evaluator.ValueEncoding
	maxWork  int
	budget   evaluator.Budget
}

// NewReader constructs a bounded reader. A non-positive limit uses the evaluator default.
func NewReader(encoding evaluator.ValueEncoding, maxWork int) *Reader {
	if nilcheck.IsNil(encoding) {
		encoding = nil
	}

	if maxWork <= 0 {
		maxWork = evaluator.DefaultMaxDataBudget
	}

	return &Reader{encoding: encoding, maxWork: maxWork}
}

// BindBudget returns a reader that also charges a request-owned budget.
// The returned reader has the concurrency guarantees of budget.
func (r *Reader) BindBudget(budget evaluator.Budget) FieldReader {
	cloned := *r
	cloned.budget = budget

	return &cloned
}

type readBudget struct {
	parent    evaluator.Budget
	left      int
	exhausted bool
}

func (b *readBudget) Charge(n int) bool {
	if b.exhausted || n < 0 || n > b.left || (b.parent != nil && !b.parent.Charge(n)) {
		b.exhausted = true
		return false
	}

	b.left -= n

	return true
}

// Lookup implements FieldReader. Each call owns its work budget.
func (r *Reader) Lookup(data map[string]any, path string) (any, bool, error) {
	if r.encoding == nil {
		// Without an encoding the budget never reaches an interface, so it stays on the stack.
		budget := readBudget{left: r.maxWork, parent: r.budget}

		v, ok := r.lookupPlain(data, path, false, &budget)
		if budget.exhausted {
			return nil, false, filter.ErrDataLimitExceeded
		}

		return v, ok, nil
	}

	budget := &readBudget{left: r.maxWork, parent: r.budget}

	v, ok := r.lookup(data, path, false, budget)
	if budget.exhausted {
		return nil, false, filter.ErrDataLimitExceeded
	}

	return v, ok, nil
}

// nextSegment splits the first dotted segment off path, as strings.Split would: the remaining path is done when path has no
// separator left.
func nextSegment(path string) (segment, rest string, done bool) {
	segment, rest, more := strings.Cut(path, ".")
	return segment, rest, !more
}

// lookupPlain is lookup for a reader without an encoding. path holds the segments not read yet; done reports that none are left.
func (r *Reader) lookupPlain(value any, path string, done bool, b *readBudget) (any, bool) {
	if !b.Charge(1) {
		return nil, false
	}

	if value == nil {
		return nil, done
	}

	if m, ok := value.(map[string]any); ok {
		if done {
			return value, true
		}

		return lookupIndex(r, m, path, b)
	}

	if m, ok := value.(map[string]string); ok && !done {
		return lookupIndex(r, m, path, b)
	}

	rv := reflect.ValueOf(value)
	derefed := false

	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if !b.Charge(1) {
			return nil, false
		}

		if rv.IsNil() {
			return nil, done
		}

		rv, derefed = rv.Elem(), true
	}

	if done {
		if !derefed {
			return value, true
		}

		return rv.Interface(), true
	}

	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, false
		}

		segment, rest, last := nextSegment(path)

		child := rv.MapIndex(mapKey(rv, segment))
		if !child.IsValid() {
			return nil, false
		}

		return r.lookupPlain(child.Interface(), rest, last, b)
	case reflect.Slice, reflect.Array:
		return r.lookupEachPlain(rv, path, b)
	default:
		return nil, false
	}
}

// lookupIndex reads the next segment of path from a map without reflection.
func lookupIndex[V any](r *Reader, m map[string]V, path string, b *readBudget) (any, bool) {
	segment, rest, last := nextSegment(path)

	child, found := m[segment]
	if !found {
		return nil, false
	}

	return r.lookupPlain(child, rest, last, b)
}

// lookupEachPlain reads path from every element of the slice or array rv and collects the values found.
func (r *Reader) lookupEachPlain(rv reflect.Value, path string, b *readBudget) (any, bool) {
	var out []any

	for i := 0; i < rv.Len(); i++ {
		if !b.Charge(1) {
			return nil, false
		}

		if v, ok := r.lookupPlain(rv.Index(i).Interface(), path, false, b); ok {
			out = append(out, v)
		}
	}

	return out, len(out) > 0
}

// mapKey returns segment as a key of the string-keyed map rv.
func mapKey(rv reflect.Value, segment string) reflect.Value {
	key := reflect.ValueOf(segment)
	if t := rv.Type().Key(); t != key.Type() {
		key = key.Convert(t)
	}

	return key
}

func (r *Reader) lookup(value any, path string, done bool, b *readBudget) (any, bool) {
	if !b.Charge(1) {
		return nil, false
	}

	if value == nil {
		return nil, done
	}

	value = r.decodeStored(value, b)

	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if !b.Charge(1) {
			return nil, false
		}

		if rv.IsNil() {
			return nil, done
		}

		rv = rv.Elem()
	}

	if !rv.IsValid() {
		return nil, done
	}

	if rv = r.decodeAddressed(rv, b); !rv.IsValid() {
		return nil, done
	}

	if done {
		return rv.Interface(), true
	}

	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		segment, rest, last := nextSegment(path)

		child := rv.MapIndex(mapKey(rv, segment))
		if !child.IsValid() {
			return nil, false
		}

		return r.lookup(child.Interface(), rest, last, b)
	}

	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		var out []any

		for i := 0; i < rv.Len(); i++ {
			if !b.Charge(1) {
				return nil, false
			}

			if v, ok := r.lookup(rv.Index(i).Interface(), path, done, b); ok {
				out = append(out, v)
			}
		}

		return out, len(out) > 0
	}

	return nil, false
}

// decodeAddressed reads rv, a struct reached through a pointer, as the encoding stores it, as the evaluator reads it: from its
// address, unless the encoding owns its type or it has a fixed form.
func (r *Reader) decodeAddressed(rv reflect.Value, b *readBudget) reflect.Value {
	if r.encoding == nil || rv.Kind() != reflect.Struct || !rv.CanAddr() {
		return rv
	}

	if t := rv.Type(); t != timeType && t != decimalType && !r.encoding.Owns(t) {
		return reflect.ValueOf(r.encoding.Encode(rv.Addr().Interface(), b))
	}

	return reflect.ValueOf(r.decodeStored(rv.Interface(), b))
}

// decodeStored returns value as the encoding stores it. A base form is in its stored form already and is never passed to the
// encoding ([evaluator.ValueEncoding]), so a document or a list the encoding returned is read as it is at every later step instead
// of being encoded again.
func (r *Reader) decodeStored(value any, b *readBudget) any {
	if r.encoding != nil && !baseForm(value) {
		if stored, handled := r.encoding.Stored(value, b); handled {
			value = stored
		} else {
			rv := reflect.ValueOf(value)
			if rv.Kind() == reflect.Struct || (rv.Kind() == reflect.Pointer && r.encoding.EncodesItself(rv.Type())) {
				value = r.encoding.Encode(value, b)
			}
		}
	}

	return value
}

// baseForm reports whether v is in a base form, which [evaluator.ValueEncoding.Stored] is not given.
func baseForm(v any) bool {
	switch v.(type) {
	case nil, string, bool, int64, int32, int, uint64, float64, float32, time.Time, decimal.Decimal, []byte, []any, map[string]any:
		return true
	default:
		return false
	}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"reflect"
	"slices"
	"strings"
)

// Reader reads the data of one evaluation under a data budget ([WithMaxDataBudget]): it converts values to their stored form
// ([StoredValue]) through a [ValueEncoding], resolves dotted paths the way a document store does, and charges every step to the
// budget, so that a profile implementing its own [Program] reads data exactly as the default evaluator does. Once the budget is
// exhausted every later charge fails, the work in progress stops, and [Reader.Err] returns [filter.ErrDataLimitExceeded].
//
// A Reader is the mutable state of one evaluation: [Reader.Reset] prepares it for a data map, [Reader.Release] drops the
// references to it; it is not safe for concurrent use. The top-level values it converts are memoized until the next Reset, so a
// value read by several predicates is converted once.
type Reader struct {
	data   map[string]any
	budget dataBudget
	roots  []rootValue // the stored forms of the top-level data values read so far ([Reader.rootStored])
	limit  int         // the budget Reset started with, which [Reader.Err] reports
	// goValues selects the reading of the default evaluator: a value as the Go value it is ([nonNullStored]) and paths through
	// map[string]any only, instead of the stored forms ([storedValue]).
	goValues bool
}

// rootValue is the stored form of the top-level data value at key.
type rootValue struct {
	key    string
	stored any
}

// Reset prepares r to read data in its stored form ([StoredValue]) through enc (nil for none), under a budget of maxWork units,
// keeping the capacity of its memo.
func (r *Reader) Reset(data map[string]any, maxWork int, enc ValueEncoding) {
	r.reset(data, maxWork, enc, false)
}

// reset is [Reader.Reset]; goValues selects the reading of the default evaluator.
func (r *Reader) reset(data map[string]any, maxWork int, enc ValueEncoding, goValues bool) {
	clear(r.roots)
	*r = Reader{data: data, budget: dataBudget{left: maxWork, enc: enc}, roots: r.roots[:0], limit: maxWork, goValues: goValues}
}

// Release drops the references r holds to the data and the values it read, keeping the capacity of its memo for the next
// [Reader.Reset].
func (r *Reader) Release() {
	clear(r.roots)
	*r = Reader{roots: r.roots[:0]}
}

// Data returns the data map r reads.
func (r *Reader) Data() map[string]any { return r.data }

// Encoding returns the [ValueEncoding] r reads stored forms through, nil without one.
func (r *Reader) Encoding() ValueEncoding { return r.budget.enc }

// Charge spends units of the budget, or exhausts it and reports false when fewer are left. A Reader is the [Budget] a
// [ValueEncoding] charges.
func (r *Reader) Charge(units int) bool { return r.budget.charge(units) }

// ChargeScan spends the scan of a string or binary of n bytes.
func (r *Reader) ChargeScan(n int) bool { return r.budget.chargeScan(n) }

// ChargeRegex spends matching a pattern of patternLen bytes against a string of n bytes.
func (r *Reader) ChargeRegex(n, patternLen int) bool { return r.budget.chargeRegex(n, patternLen) }

// ChargeComparison spends comparing the data value a with c: a unit, more for bytes compared one by one and for a decimal.
func (r *Reader) ChargeComparison(a, c any) bool { return r.budget.chargeComparison(a, c) }

// Exhausted reports whether the budget has run out.
func (r *Reader) Exhausted() bool { return r.budget.exhausted }

// Err returns [filter.ErrDataLimitExceeded] once the budget is exhausted, nil before.
func (r *Reader) Err() error {
	if !r.budget.exhausted {
		return nil
	}

	return dataLimitError(r.limit)
}

// Stored returns v in its stored form ([StoredValue]), charging the conversion to the budget.
func (r *Reader) Stored(v any) any {
	if r.goValues {
		return nonNullStored(v, &r.budget)
	}

	return storedValue(v, &r.budget)
}

// rootStored returns the stored form of v, the top-level data value at key. A value of a type without a fixed form (a struct, a
// typed map or slice, a pointer) is converted once per evaluation, whatever the number of predicates that read paths into it; a
// common form is cheap to read and is not memoized. A conversion the data budget cut short is not memoized either.
func (r *Reader) rootStored(key string, v any) any {
	if plainType(v) {
		return r.Stored(v)
	}

	for _, root := range r.roots {
		if root.key == key {
			return root.stored
		}
	}

	s := r.Stored(v)
	if !r.budget.exhausted {
		r.roots = append(r.roots, rootValue{key: key, stored: s})
	}

	return s
}

// WalkField resolves the dotted path in the data map without stepping into lists: present reports whether the full path exists,
// value holds the value at it (nil when absent). Each step reads a document in any form a store writes as one, and the value is
// in its stored form ([StoredValue]); under the default evaluator a step reads only a map[string]any and the value is the Go value
// the data holds. Each step is charged to the budget.
func (r *Reader) WalkField(path string) (value any, present bool) {
	var current any = r.data
	for part := range strings.SplitSeq(path, ".") {
		if !r.budget.charge(1 + len(part)/BytesPerUnit) { // a long key costs its hashing
			return nil, false
		}

		if !r.goValues {
			current = storedValue(current, &r.budget)
		}

		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	if !r.goValues {
		return storedValue(current, &r.budget), true
	}

	return current, true
}

// ReadPath returns the value at path as [Reader.WalkField] reads it, nil when absent; a path of one step under the default
// evaluator is one lookup.
func (r *Reader) ReadPath(path string) any {
	if !r.goValues || strings.IndexByte(path, '.') >= 0 {
		v, _ := r.WalkField(path)
		return v
	}

	if !r.budget.charge(1 + len(path)/BytesPerUnit) {
		return nil
	}

	return r.data[path]
}

// ResolvePath resolves a dotted path in data the way a MongoDB aggregation path does: a step into a list of documents resolves the
// rest of the path in each document and yields the list of the values found (crossed), skipping the documents that lack it and the
// elements that are not documents. present reports whether the path exists — in at least one document of a list. Values are read in
// their stored form ([Reader.Stored]), so a typed slice is a list and a bson.D a document, as a store writes them. Every step and
// every element of a list is charged to the budget, so a path through a value that shares its parts (a map holding a list of
// itself) stops when the budget runs out instead of multiplying; what it resolved by then is not an answer.
func (r *Reader) ResolvePath(data any, path string) (value any, present, crossed bool) {
	cur := data

	root := r.isRoot(data)
	for {
		if !r.budget.charge(1) {
			return nil, false, false
		}

		switch x := r.Stored(cur).(type) {
		case map[string]any:
			head, rest, more := strings.Cut(path, ".")
			if !r.budget.charge(len(head) / BytesPerUnit) { // a long key costs its hashing
				return nil, false, false
			}

			v, ok := x[head]
			if !ok {
				return nil, false, false
			}

			if root {
				v, root = r.rootStored(head, v), false
				if !more {
					return v, true, false
				}
			} else if !more {
				return r.Stored(v), true, false
			}

			cur, path = v, rest
		case []any:
			return r.resolveInList(x, path)
		default:
			return nil, false, false
		}
	}
}

// resolveInList is [Reader.ResolvePath] for a step into the list x: it resolves path in each document of x and yields the list
// of the values found, skipping the documents that lack it and the elements that are not documents.
func (r *Reader) resolveInList(x []any, path string) (value any, present, crossed bool) {
	if !r.budget.charge(len(x)) {
		return nil, false, false
	}

	out := make([]any, 0, len(x))
	for _, el := range x {
		if doc, isDoc := r.Stored(el).(map[string]any); isDoc {
			// A document whose rest of the path crosses a list yields that list, empty or not.
			if v, ok, inner := r.ResolvePath(doc, path); ok || inner {
				present = present || ok

				out = append(out, v)
			}
		}

		if r.budget.exhausted {
			return nil, false, false
		}
	}

	return out, present, true
}

// QueryValues appends to out the values a MongoDB query reaches at path in cur: a list of documents on the way is traversed into
// each document (other elements yield nothing), a document or value that lacks the rest of the path yields null, and with flatten a
// list at the end of the path yields its elements instead of itself. Every value is read and yielded in its stored form
// ([Reader.Stored]). Every step and every element is charged to the budget, as in [Reader.ResolvePath].
func (r *Reader) QueryValues(cur any, path string, flatten bool, out []any) []any {
	if !r.budget.charge(1) {
		return out
	}

	switch x := r.Stored(cur).(type) {
	case map[string]any:
		return r.queryDocValues(cur, x, path, flatten, out)
	case []any:
		if !r.budget.charge(len(x)) {
			return out
		}

		for _, el := range x {
			if doc, isDoc := r.Stored(el).(map[string]any); isDoc {
				out = r.QueryValues(doc, path, flatten, out)
			}

			if r.budget.exhausted {
				return out
			}
		}

		return out
	default:
		return append(out, nil)
	}
}

// queryDocValues is [Reader.QueryValues] on the document x (the stored form of cur): it steps into the first path segment.
func (r *Reader) queryDocValues(cur any, x map[string]any, path string, flatten bool, out []any) []any {
	head, rest, more := strings.Cut(path, ".")
	if !r.budget.charge(len(head) / BytesPerUnit) { // a long key costs its hashing
		return out
	}

	v, ok := x[head]

	root := ok && r.isRoot(cur)
	if root {
		v = r.rootStored(head, v)
	}

	switch {
	case !ok:
		return append(out, nil)
	case more:
		return r.QueryValues(v, rest, flatten, out)
	case !root:
		v = r.Stored(v)
	}

	list, isList := v.([]any)
	if !isList || !flatten {
		return append(out, v)
	}

	if !r.budget.charge(len(list)) {
		return out
	}

	out = slices.Grow(out, len(list))
	for _, el := range list {
		out = append(out, r.Stored(el))
	}

	return out
}

// isRoot reports whether data is the top-level data map r reads, whose values [Reader.rootStored] memoizes. It compares the maps
// by identity: a nested document is never the root, even when its keys are the same.
func (r *Reader) isRoot(data any) bool {
	m, ok := data.(map[string]any)
	return ok && m != nil && len(m) == len(r.data) && reflect.ValueOf(m).UnsafePointer() == reflect.ValueOf(r.data).UnsafePointer()
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

import (
	"fmt"
	"strings"
)

// JSON names of the standard batch request fields (AIP-231 to AIP-235).
const (
	// FieldRequests is the repeated field of nested standard requests.
	FieldRequests = "requests"
	// FieldNames is the repeated field of resource names (Batch Get and
	// Batch Delete).
	FieldNames = "names"
	// FieldParent is the parent shared by the items of the batch.
	FieldParent = "parent"
)

// wildcard is the parent segment that matches any value (AIP-159), accepted
// when a batch spans parents.
const wildcard = "-"

// CheckSize checks the item count n of the repeated field (its JSON name,
// such as [FieldRequests]) before any other work: more than [WithMaxItems]
// items is [ErrTooManyItems], none is [ErrEmpty] unless [WithAllowEmpty]
// is set. Both are a [coreerrs.FieldError] on field.
func CheckSize(field string, n int, opts ...Option) error {
	return newValidateOptions(opts...).checkSize(field, n)
}

func (o *validateOptions) checkSize(field string, n int) error {
	if o.maxItems > 0 && n > o.maxItems {
		return fieldError(field, fmt.Sprintf("at most %d items are allowed, got %d", o.maxItems, n), ErrTooManyItems)
	}

	if n == 0 && !o.allowEmpty {
		return fieldError(field, "is required", ErrEmpty)
	}

	return nil
}

// CheckParent checks that every item of the repeated field names a parent
// matching parent, the parent of the batch request (AIP-231 to AIP-235).
// An empty parent checks nothing. parentOf returns the parent an item names
// and the JSON path, relative to the item, of the field it comes from: ""
// for a resource name of "names", "parent" for a nested Create request,
// "book.name" for a nested Update request. An item without a parent (an
// empty string) inherits the batch parent and passes.
//
// The first mismatch is an *[ItemError] holding a [coreerrs.FieldError] that
// wraps [ErrParentMismatch], reported on "names[i]" or
// "requests[i].<path>". A "-" segment of parent matches any segment
// ([ParentMatches]).
func CheckParent[T any](field string, items []T, parent string, parentOf func(item T) (itemParent, path string)) error {
	if parent == "" || parentOf == nil {
		return nil
	}

	for i, item := range items {
		p, path := parentOf(item)
		if p != "" && !ParentMatches(parent, p) {
			return NewItemError(field, i, fieldError(path, "does not match the parent of the batch request", ErrParentMismatch))
		}
	}

	return nil
}

// ParentMatches reports whether the parent named by an item matches parent,
// the parent of a batch request: the two have the same segments, where a
// "-" segment of parent matches any segment (a batch spanning parents).
func ParentMatches(parent, itemParent string) bool {
	if parent == itemParent {
		return true
	}

	want, got := strings.Split(parent, "/"), strings.Split(itemParent, "/")
	if len(want) != len(got) {
		return false
	}

	for i, w := range want {
		if w != wildcard && w != got[i] {
			return false
		}
	}

	return true
}

// CheckHoisted checks a field hoisted to the batch request (AIP-231 to
// AIP-235): when both the batch value top and the value an item sets are
// non-zero they must be equal. nested returns the value of an item and the
// JSON path of its field relative to the item. The first mismatch is an
// *[ItemError] holding a [coreerrs.FieldError] that wraps
// [ErrHoistedMismatch], reported on "requests[i].<path>". A zero top checks
// nothing.
func CheckHoisted[T any, V comparable](field string, items []T, top V, nested func(item T) (value V, path string)) error {
	var zero V
	if top == zero || nested == nil {
		return nil
	}

	for i, item := range items {
		v, path := nested(item)
		if v != zero && v != top {
			return NewItemError(field, i, fieldError(path, "does not match the value of the batch request", ErrHoistedMismatch))
		}
	}

	return nil
}

// CheckUnique rejects two items naming the same resource. keyOf returns the
// identity of an item (its resource name or id) and the JSON path of the
// field holding it, relative to the item; a zero key is skipped. The first
// duplicate is an *[ItemError] at the later index holding a
// [coreerrs.FieldError] that wraps [ErrDuplicate].
//
// AIP-231 to AIP-235 do not forbid duplicates; reject them where a repeated
// key is ambiguous (two updates of one resource) or bound to fail (a second
// delete), and let a Batch Get return the resource once per name ([Get]).
func CheckUnique[T any, K comparable](field string, items []T, keyOf func(item T) (key K, path string)) error {
	if keyOf == nil {
		return nil
	}

	var zero K

	seen := make(map[K]int, len(items))

	for i, item := range items {
		k, path := keyOf(item)
		if k == zero {
			continue
		}

		if first, ok := seen[k]; ok {
			return NewItemError(field, i, fieldError(path, fmt.Sprintf("duplicates %s[%d]", field, first), ErrDuplicate))
		}

		seen[k] = i
	}

	return nil
}

// Each runs check on every item in order and returns the first failure as
// the *[ItemError] of its index, so a field error of a nested request
// ("book.title") is reported as "requests[i].book.title". Use it to apply
// the validation of the singular method to each nested request.
func Each[T any](field string, items []T, check func(i int, item T) error) error {
	for i, item := range items {
		if err := check(i, item); err != nil {
			return NewItemError(field, i, err)
		}
	}

	return nil
}

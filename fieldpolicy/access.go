// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy

// Access is a shared, immutable field authorization policy. Implementations must
// be safe for concurrent use. AllowList implements this contract.
type Access interface{ Allows(field string) bool }

// AsAllowList returns the AllowList behind access, supplied by value or pointer.
// A nil *AllowList is recognized as the zero AllowList. Other Access implementations,
// including a nil interface, return the zero AllowList and false; their restrictions
// cannot be inspected through this function.
func AsAllowList(access Access) (AllowList, bool) {
	switch list := access.(type) {
	case AllowList:
		return list, true
	case *AllowList:
		if list == nil {
			return AllowList{}, true
		}

		return *list, true
	default:
		return AllowList{}, false
	}
}

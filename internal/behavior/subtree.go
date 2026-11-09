// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SubtreeCacheSize bounds the (message type, behavior set) pairs whose
// [SubtreeHasAny] result is cached. Past the bound the least recently used
// entry is evicted, so dynamic descriptors are never retained without limit.
const SubtreeCacheSize = 4096

// subtreeKey identifies a cached [SubtreeHasAny] result: the message type and
// the behavior set as a bitmask of the values below 64.
type subtreeKey struct {
	md   protoreflect.MessageDescriptor
	mask uint64
}

var subtreeCache = mustCache[subtreeKey, bool](SubtreeCacheSize)

// SubtreeHasAny reports whether a field reachable from md carries any of the
// behaviors in set. All enum values, including custom values, participate in
// the check. Results for sets of values below 64 (every standard behavior) are
// cached per message type (see [SubtreeCacheSize]); other sets are searched on
// every call.
func SubtreeHasAny(md protoreflect.MessageDescriptor, set ...annotations.FieldBehavior) bool {
	if md == nil || len(set) == 0 {
		return false
	}

	mask, ok := setMask(set)
	if !ok {
		return subtreeSearch(md, set)
	}

	key := subtreeKey{md: md, mask: mask}
	if v, hit := subtreeCache.Get(key); hit {
		return v
	}

	v := subtreeSearch(md, set)
	subtreeCache.Put(key, v)

	return v
}

// setMask returns the bitmask of set, false when a value does not fit.
func setMask(set []annotations.FieldBehavior) (uint64, bool) {
	var mask uint64

	for _, b := range set {
		if b < 0 || b >= 64 {
			return 0, false
		}

		mask |= 1 << uint(b)
	}

	return mask, true
}

// subtreeSearch walks the schema reachable from md with a call-local visited
// set, so recursive types terminate.
func subtreeSearch(md protoreflect.MessageDescriptor, set []annotations.FieldBehavior) bool {
	visited := make(map[protoreflect.MessageDescriptor]bool)

	var visit func(protoreflect.MessageDescriptor) bool

	visit = func(md protoreflect.MessageDescriptor) bool {
		if md == nil || visited[md] {
			return false
		}

		visited[md] = true

		fields := md.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)
			if HasAny(fd, set...) || (fd.Message() != nil && visit(fd.Message())) {
				return true
			}
		}

		return false
	}

	return visit(md)
}

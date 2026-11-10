// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskwalk

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// Filter recursively applies the filter mask msk to msg: unmatched fields
// are cleared, matched-leaf fields are kept as-is, matched-branch fields
// recurse. The traversal state machine lives in [walk]; this function only
// supplies the filter-side action callbacks.
//
// With dropUnaddressed (the read mask of fieldmask.FieldMask.Filter) a value
// of dynamic JSON the mask addresses nothing of is dropped; without it (the
// cleaning of an update request) it is left as is.
func Filter[M ~map[string]M](msk M, msg proto.Message, dropUnaddressed bool) {
	filterWith(msk, msg, dynamicMask[M]{dropUnaddressed: dropUnaddressed})
}

// filterWith is [Filter] applying dyn to dynamic JSON.
func filterWith[M ~map[string]M](msk M, msg proto.Message, dyn dynamicMask[M]) {
	walk(msk, msg, maskOp[M]{
		onUnmatched:         func(prf protoreflect.Message, fd protoreflect.FieldDescriptor) { prf.Clear(fd) },
		onLeaf:              func(protoreflect.Message, protoreflect.FieldDescriptor) {},
		recurse:             func(n M, m proto.Message) { filterWith(n, m, dyn) },
		onListWildcardLeaf:  func(protoreflect.Message, protoreflect.FieldDescriptor) {},
		onMapEntryUnmatched: func(mm protoreflect.Map, k protoreflect.MapKey) { mm.Clear(k) },
		onMapEntryLeaf:      func(protoreflect.Map, protoreflect.MapKey) {},
		dynamic:             dyn,

		dropsUnmatchedEntries: true,
	})
}

// Prune recursively applies the prune mask msk to msg: matched-leaf fields
// are cleared, matched-branch fields recurse with the sub-mask, unmatched
// fields are left untouched. Mirror of [Filter]: both share the [walk] state
// machine and only differ in their action callbacks.
func Prune[M ~map[string]M](msk M, msg proto.Message) {
	walk(msk, msg, maskOp[M]{
		onUnmatched:         func(protoreflect.Message, protoreflect.FieldDescriptor) {},
		onLeaf:              func(prf protoreflect.Message, fd protoreflect.FieldDescriptor) { prf.Clear(fd) },
		recurse:             func(n M, m proto.Message) { Prune(n, m) },
		onListWildcardLeaf:  func(prf protoreflect.Message, fd protoreflect.FieldDescriptor) { prf.Clear(fd) },
		onMapEntryUnmatched: func(protoreflect.Map, protoreflect.MapKey) {},
		onMapEntryLeaf:      func(mm protoreflect.Map, k protoreflect.MapKey) { mm.Clear(k) },
		dynamic:             dynamicMask[M]{prune: true},
	})
}

// applyToMessageList applies the "*" sub-mask nested to every element of the
// repeated message field fd.
func applyToMessageList[M ~map[string]M](fd protoreflect.FieldDescriptor, lst protoreflect.List, nested M, apply func(M, proto.Message)) {
	if lst.Len() == 0 || fd.Kind() != protoreflect.MessageKind || fd.Message() == nil {
		return
	}

	_ = behavior.ForEachMessageInList(lst, func(_ int, m protoreflect.Message) error {
		apply(nested, m.Interface())
		return nil
	})
}

// Insert inserts the pre-split segments parts into mask, a leaf dominating
// a branch: a leaf overwrites an existing branch, and a path below an
// existing leaf is redundant.
func Insert[M ~map[string]M](mask M, parts []string) {
	if len(parts) == 0 {
		return
	}

	head := parts[0]

	if len(parts) == 1 {
		mask[head] = nil
		return
	}

	nested, exists := mask[head]
	if exists && nested == nil {
		return
	}

	if !exists {
		nested = make(M)
		mask[head] = nested
	}

	Insert(nested, parts[1:])
}

// CoveredAt returns the number of leading segments of parts that end at a
// leaf of mask before parts itself ends: the path of parts was inserted but
// a shorter path covers it, so mask no longer holds it. It returns 0 when no
// proper prefix of parts is a leaf of mask.
func CoveredAt[M ~map[string]M](mask M, parts []string) int {
	level := mask

	for i := range len(parts) - 1 {
		nested, ok := level[parts[i]]
		if !ok {
			return 0
		}

		if nested == nil {
			return i + 1
		}

		level = nested
	}

	return 0
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskwalk

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
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
	FilterWith(msk, msg, dynamicMask[M]{dropUnaddressed: dropUnaddressed}.hooks(), KeyMask[M])
}

// FilterWith applies [Filter]'s traversal with the supplied dynamic handling
// and map-key lookup. The update reference walk uses the a8b17ba callbacks
// so that sharing traversal does not change its dynamic-message semantics.
// All callbacks are required and are retained throughout recursion.
func FilterWith[M ~map[string]M](msk M, msg proto.Message, dyn DynamicHooks[M], keyMask func(M, string) (M, bool)) {
	walk(msk, msg, maskOp[M]{
		onUnmatched:         func(prf protoreflect.Message, fd protoreflect.FieldDescriptor) { prf.Clear(fd) },
		onLeaf:              func(protoreflect.Message, protoreflect.FieldDescriptor) {},
		recurse:             func(n M, m proto.Message) { FilterWith(n, m, dyn, keyMask) },
		onListWildcardLeaf:  func(protoreflect.Message, protoreflect.FieldDescriptor) {},
		onMapEntryUnmatched: func(mm protoreflect.Map, k protoreflect.MapKey) { mm.Clear(k) },
		onMapEntryLeaf:      func(protoreflect.Map, protoreflect.MapKey) {},
		dynamic:             dyn,
		keyMask:             keyMask,

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
		dynamic:             dynamicMask[M]{prune: true}.hooks(),
		keyMask:             KeyMask[M],
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

// KeyMask returns the sub-mask nested, the sub-mask of a map field, names
// the map entry key with, and whether it names it by key. The mask key "*"
// is the wildcard, never a literal key. A quoted JSON "*" key has the distinct
// LiteralStar representation; a protobuf map still rejects that key path.
func KeyMask[M ~map[string]M](nested M, key string) (M, bool) {
	if key == maskpath.Wildcard {
		key = maskpath.LiteralStar
	}

	sub, ok := nested[key]

	return sub, ok
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

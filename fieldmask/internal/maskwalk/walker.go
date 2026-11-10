// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskwalk

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
)

// maskOp carries the per-decision callbacks that customize how
// [walk] reacts to each field of a message.
//
// [Filter] and [Prune] are mirror operations
// over the same traversal state machine — they share ProtoReflect Range,
// list/map dispatch, AIP-161 wildcard semantics, and dynamic well-known type
// short-circuiting — and differ only in what "matched" and "unmatched" mean.
// maskOp expresses that delta as a handful of callbacks instead of two
// near-identical 90-line functions.
//
// All callbacks are required; the walker does not check for nil. The
// dynamic JSON of a google.protobuf.Struct, Value or ListValue message is
// handed to dynamic with the mask the walker was invoked with.
type maskOp[M ~map[string]M] struct {
	// onUnmatched fires for a field that is not present in the mask at this
	// level. Filter clears the field; Prune is a no-op.
	onUnmatched func(prf protoreflect.Message, fd protoreflect.FieldDescriptor)

	// onLeaf fires for a field that is present in the mask with a nil
	// sub-mask. Filter keeps it; Prune clears it.
	onLeaf func(prf protoreflect.Message, fd protoreflect.FieldDescriptor)

	// recurse dispatches into a nested message for a branch entry in the
	// mask. Filter recurses with Filter; Prune with
	// Prune.
	recurse func(nested M, msg proto.Message)

	// onListWildcardLeaf fires when the sub-mask for a list field is
	// {"*": nil}. Filter keeps the entire list; Prune
	// clears it.
	onListWildcardLeaf func(prf protoreflect.Message, fd protoreflect.FieldDescriptor)

	// onMapEntryUnmatched fires for a map entry whose key is not targeted by
	// either a specific rule or a "*" wildcard. Filter drops the
	// entry; Prune keeps it.
	onMapEntryUnmatched func(mm protoreflect.Map, k protoreflect.MapKey)

	// onMapEntryLeaf fires for a map entry whose matching sub-mask is nil
	// (leaf rule). Filter keeps it; Prune drops it.
	onMapEntryLeaf func(mm protoreflect.Map, k protoreflect.MapKey)

	// dropsUnmatchedEntries reports that onMapEntryUnmatched drops the entry
	// and onMapEntryLeaf keeps it (Filter). It lets a string-keyed map
	// whose sub-mask names keys without "*" be rebuilt from the named entries
	// instead of dropping every other entry one by one.
	dropsUnmatchedEntries bool

	// dynamic applies the mask to a google.protobuf.Struct, Value or
	// ListValue message.
	dynamic dynamicMask[M]
}

// walk iterates over the populated fields of msg, dispatching to op for each
// field based on whether it is matched, unmatched, or a leaf in the mask.
// A google.protobuf.Struct, Value or ListValue message is handed to
// op.dynamic.
func walk[M ~map[string]M](msk M, msg proto.Message, op maskOp[M]) {
	if op.dynamic.apply(msk, msg) {
		return
	}

	prf := msg.ProtoReflect()
	prf.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		nested, exists := msk[string(fd.Name())]
		if !exists {
			op.onUnmatched(prf, fd)
			return true
		}

		if nested == nil {
			op.onLeaf(prf, fd)
			return true
		}

		switch {
		case fd.IsList():
			walkList(prf, fd, nested, op)
		case fd.IsMap():
			walkMap(prf, fd, nested, op)
		case fd.Kind() == protoreflect.MessageKind:
			m := prf.Get(fd).Message()
			if !op.dynamic.keeps(nested, m) {
				op.onUnmatched(prf, fd)
				return true
			}

			op.recurse(nested, m.Interface())
		}

		return true
	})
}

// walkList applies the sub-mask nested of the list field fd to its
// elements: a "*" leaf fires onListWildcardLeaf, a "*" branch applies to
// every element. A repeated field is only addressed through "*" (AIP-161):
// an index ("aliases.0") or a field of the elements without "*"
// ("aliases.display_name") selects nothing, so a sub-mask without "*" leaves
// the list unmatched. The public entry points reject such paths before they
// get here.
func walkList[M ~map[string]M](prf protoreflect.Message, fd protoreflect.FieldDescriptor, nested M, op maskOp[M]) {
	wildcardMask, ok := nested[maskpath.Wildcard]

	switch {
	case !ok:
		op.onUnmatched(prf, fd)
	case wildcardMask == nil:
		op.onListWildcardLeaf(prf, fd)
	default:
		lst := prf.Get(fd).List()
		op.dynamic.compactList(lst, wildcardMask)
		applyToMessageList(fd, lst, wildcardMask, op.recurse)
	}
}

// walkMap applies the sub-mask nested of the map field fd to the entries of
// mm: a key's specific rule wins over "*"; an entry neither targets fires
// onMapEntryUnmatched, a leaf rule onMapEntryLeaf, and a branch rule recurses
// into a message value.
func walkMap[M ~map[string]M](prf protoreflect.Message, fd protoreflect.FieldDescriptor, nested M, op maskOp[M]) {
	mm := prf.Get(fd).Map()
	if !mm.IsValid() || mm.Len() == 0 {
		return
	}

	wildcardMask, hasWildcard := nested[maskpath.Wildcard]
	mapValueDesc := fd.MapValue()

	if !hasWildcard && op.dropsUnmatchedEntries && fd.MapKey().Kind() == protoreflect.StringKind && len(nested) < mm.Len() {
		keepNamedEntries(prf, fd, mm, nested, op)
		return
	}

	mm.Range(func(k protoreflect.MapKey, mapVal protoreflect.Value) bool {
		specificMask, hasSpecific := KeyMask(nested, k.String())

		var finalMask M

		switch {
		case hasSpecific:
			finalMask = specificMask
		case hasWildcard:
			finalMask = wildcardMask
		default:
			op.onMapEntryUnmatched(mm, k)
			return true
		}

		if finalMask == nil {
			op.onMapEntryLeaf(mm, k)
			return true
		}

		if mapValueDesc != nil && mapValueDesc.Kind() == protoreflect.MessageKind {
			if !op.dynamic.keeps(finalMask, mapVal.Message()) {
				op.onMapEntryUnmatched(mm, k)
				return true
			}

			op.recurse(finalMask, mapVal.Message().Interface())
		}

		return true
	})
}

// keepNamedEntries is [walkMap] for a filter whose sub-mask nested names the
// keys of the string-keyed map mm without "*": every other entry is dropped.
// The named entries are read by key and the field is replaced by a map
// holding only the kept ones, so the cost follows the named keys, not the
// size of the map. Leaf rules keep their entry; branch rules recurse into a
// message value, and an entry [dynamicMask.keeps] rejects is dropped, as in
// [walkMap].
func keepNamedEntries[M ~map[string]M](prf protoreflect.Message, fd protoreflect.FieldDescriptor, mm protoreflect.Map, nested M, op maskOp[M]) {
	messages := fd.MapValue().Kind() == protoreflect.MessageKind

	var kept protoreflect.Map

	for key, sub := range nested {
		k := protoreflect.ValueOfString(key).MapKey()

		v := mm.Get(k)
		if !v.IsValid() {
			continue
		}

		if sub != nil && messages {
			if !op.dynamic.keeps(sub, v.Message()) {
				continue
			}

			op.recurse(sub, v.Message().Interface())
		}

		if kept == nil {
			kept = prf.NewField(fd).Map()
		}

		kept.Set(k, v)
	}

	if kept == nil {
		prf.Clear(fd)
		return
	}

	prf.Set(fd, protoreflect.ValueOfMap(kept))
}

// dynamicMask applies a mask to dynamic JSON: the keys of a mask reaching a
// google.protobuf.Struct name its entries, as the keys of a map field do, and
// "*" every entry; below an entry the mask goes on into the Value. A Value
// holding a Struct is masked as that Struct; one holding a list takes "*"
// for every element, and keys without "*" address no element (no implicit
// per-element form). A path through a value of another shape (a scalar or
// null, a list without "*") addresses nothing: the data is dynamic, and its
// shape is no error. With prune the addressed entries are removed and such a
// value is left as it is. Otherwise only the addressed entries are kept; with
// dropUnaddressed (the read mask of fieldmask.FieldMask.Filter) a value the mask
// addresses nothing of is dropped too — the entry from its Struct, the
// element from its list, the field or map entry holding it — unless another
// path keeps it whole, while without it (the cleaning of an update request)
// such a value is left as it is.
type dynamicMask[M ~map[string]M] struct {
	prune           bool
	dropUnaddressed bool
}

// apply masks msg when it is a google.protobuf.Struct, Value or ListValue
// and reports whether it was one. A Value or ListValue the mask addresses
// nothing of is left as is: the caller drops it (see [dynamicMask.keeps]).
func (op dynamicMask[M]) apply(msk M, msg proto.Message) bool {
	switch m := msg.(type) {
	case *structpb.Struct:
		op.structFields(msk, m)
	case *structpb.Value:
		op.value(msk, m)
	case *structpb.ListValue:
		op.list(msk, m)
	default:
		return false
	}

	return true
}

// keeps reports whether the message m, held by a field, map entry or list
// element the branch msk reaches, is kept: false only with dropUnaddressed
// for a Value or ListValue msk addresses nothing of.
func (op dynamicMask[M]) keeps(msk M, m protoreflect.Message) bool {
	if !op.dropUnaddressed {
		return true
	}

	switch v := m.Interface().(type) {
	case *structpb.Value:
		return addressable(msk, v)
	case *structpb.ListValue:
		_, ok := msk[maskpath.Wildcard]
		return ok
	default:
		return true
	}
}

// compactList removes from lst, a repeated field reached by the "*" branch
// msk, the elements [dynamicMask.keeps] drops, keeping the others in order.
func (op dynamicMask[M]) compactList(lst protoreflect.List, msk M) {
	if !op.dropUnaddressed || !lst.IsValid() {
		return
	}

	n := 0

	for i := range lst.Len() {
		e := lst.Get(i)
		if m, ok := e.Interface().(protoreflect.Message); ok && !op.keeps(msk, m) {
			continue
		}

		if n != i {
			lst.Set(n, e)
		}

		n++
	}

	lst.Truncate(n)
}

// addressable reports whether the branch msk addresses anything of v: a
// Struct by its keys or "*", a list only through "*"; a scalar or null
// nothing.
func addressable[M ~map[string]M](msk M, v *structpb.Value) bool {
	switch {
	case v.GetStructValue() != nil:
		return true
	case v.GetListValue() != nil:
		_, ok := msk[maskpath.Wildcard]
		return ok
	default:
		return false
	}
}

// structFields masks the entries of s; a key's own rule wins over "*".
func (op dynamicMask[M]) structFields(msk M, s *structpb.Struct) {
	wildcard, hasWildcard := msk[maskpath.Wildcard]

	for key, val := range s.GetFields() {
		sub, named := msk[key]

		switch {
		case named:
		case hasWildcard:
			sub = wildcard
		default:
			if !op.prune {
				delete(s.Fields, key)
			}

			continue
		}

		if sub == nil {
			if op.prune {
				delete(s.Fields, key)
			}

			continue
		}

		if !op.value(sub, val) && op.dropUnaddressed {
			delete(s.Fields, key)
		}
	}
}

// value masks the Struct or list v holds and reports whether msk addresses
// anything of v ([addressable]); any other value is left as is.
func (op dynamicMask[M]) value(msk M, v *structpb.Value) bool {
	if s := v.GetStructValue(); s != nil {
		op.structFields(msk, s)
		return true
	}

	if lv := v.GetListValue(); lv != nil {
		return op.list(msk, lv)
	}

	return false
}

// list masks the elements of lv through the "*" of msk and reports whether
// msk has one: a "*" leaf keeps them all, or removes them all with prune; a
// "*" branch masks every element, and with dropUnaddressed drops the
// elements it addresses nothing of, keeping the others in order.
func (op dynamicMask[M]) list(msk M, lv *structpb.ListValue) bool {
	sub, ok := msk[maskpath.Wildcard]

	switch {
	case !ok:
		return false
	case sub == nil:
		if op.prune {
			lv.Values = nil
		}
	default:
		kept := lv.Values[:0]

		for _, e := range lv.GetValues() {
			if op.value(sub, e) || !op.dropUnaddressed {
				kept = append(kept, e)
			}
		}

		clear(lv.Values[len(kept):])
		lv.Values = kept
	}

	return true
}

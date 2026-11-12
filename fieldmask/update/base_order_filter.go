// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

// This is the a8b17ba reference order, used only by the reference walk, never
// for acceptance. Filter traversal is shared with maskwalk.FilterWith; the
// callbacks retain the base type switches and literal-star key handling.

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// baseOrderFilter applies the shared traversal with the base dynamic callbacks.
func baseOrderFilter[M ~map[string]M](msk M, msg proto.Message, dropUnaddressed bool) {
	dyn := baseOrderDynamicMask[M]{dropUnaddressed: dropUnaddressed}
	maskwalk.FilterWith(msk, msg, maskwalk.DynamicHooks[M]{
		Apply: dyn.apply, Keeps: dyn.keeps, CompactList: dyn.compactList,
	}, baseOrderKeyMask[M])
}

// baseOrderKeyMask returns the sub-mask nested, the sub-mask of a map field, names
// the map entry key with, and whether it names it by key. The mask key "*"
// is the wildcard, never a literal key (AIP-161 cannot address a literal "*"
// key), so an entry whose key is "*" is never named: only the wildcard
// reaches it.
func baseOrderKeyMask[M ~map[string]M](nested M, key string) (M, bool) {
	if key == maskpath.Wildcard {
		return nil, false
	}

	sub, ok := nested[key]

	return sub, ok
}

// baseOrderDynamicMask applies a mask to dynamic JSON: the keys of a mask reaching a
// google.protobuf.Struct name its entries, as the keys of a map field do, and
// "*" every entry; below an entry the mask goes on into the Value. A Value
// holding a Struct is masked as that Struct; one holding a list takes "*"
// for every element, and keys without "*" address no element (no implicit
// per-element form). A path through a value of another shape (a scalar or
// null, a list without "*") addresses nothing: the data is dynamic, and its
// shape is no error. With prune the addressed entries are removed and such a
// value is left as it is. Otherwise only the addressed entries are kept; with
// dropUnaddressed (the read mask of fieldmask.FieldMask.baseOrderFilter) a value the mask
// addresses nothing of is dropped too — the entry from its Struct, the
// element from its list, the field or map entry holding it — unless another
// path keeps it whole, while without it (the cleaning of an update request)
// such a value is left as it is.
type baseOrderDynamicMask[M ~map[string]M] struct {
	prune           bool
	dropUnaddressed bool
}

// apply masks msg when it is a google.protobuf.Struct, Value or ListValue
// and reports whether it was one. A Value or ListValue the mask addresses
// nothing of is left as is: the caller drops it (see [baseOrderDynamicMask.keeps]).
func (op baseOrderDynamicMask[M]) apply(msk M, msg proto.Message) bool {
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
func (op baseOrderDynamicMask[M]) keeps(msk M, m protoreflect.Message) bool {
	if !op.dropUnaddressed {
		return true
	}

	switch v := m.Interface().(type) {
	case *structpb.Value:
		return baseOrderAddressable(msk, v)
	case *structpb.ListValue:
		_, ok := msk[maskpath.Wildcard]
		return ok
	default:
		return true
	}
}

// compactList removes from lst, a repeated field reached by the "*" branch
// msk, the elements [baseOrderDynamicMask.keeps] drops, keeping the others in order.
func (op baseOrderDynamicMask[M]) compactList(lst protoreflect.List, msk M) {
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

// baseOrderAddressable reports whether the branch msk addresses anything of v: a
// Struct by its keys or "*", a list only through "*"; a scalar or null
// nothing.
func baseOrderAddressable[M ~map[string]M](msk M, v *structpb.Value) bool {
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
func (op baseOrderDynamicMask[M]) structFields(msk M, s *structpb.Struct) {
	wildcard, hasWildcard := msk[maskpath.Wildcard]

	for key, val := range s.GetFields() {
		sub, named := baseOrderKeyMask(msk, key)

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
// anything of v ([baseOrderAddressable]); any other value is left as is.
func (op baseOrderDynamicMask[M]) value(msk M, v *structpb.Value) bool {
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
func (op baseOrderDynamicMask[M]) list(msk M, lv *structpb.ListValue) bool {
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

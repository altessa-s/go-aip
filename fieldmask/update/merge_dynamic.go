// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// The merge of a mask into dynamic JSON (see [MergePaths]): the keys of a
// mask reaching a google.protobuf.Struct name its entries and "*" every entry
// the source holds; below an entry the mask goes on into the Value. A key
// named whole is replaced from the source, or deleted when the source lacks
// it. A missing or non-Struct destination value on the way is replaced by a
// new Struct only when the source holds a Struct there with something the
// mask addresses. "*" on a list value updates the destination elements in
// place by index, as on a repeated field, so the source must hold as many
// elements as the destination or none ([checkDynamicCounts]).

// mergeDynamicField applies nested, the sub-mask of the google.protobuf.Struct,
// Value or ListValue field fd, from s to d; s is a private copy of the source
// (nil when it holds nothing). The field is set in d only when the merge
// writes something.
func mergeDynamicField(d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask) {
	var (
		dv   *structpb.Value
		live bool
	)

	if d.Has(fd) {
		dv, live = dynamicValueOf(d.Get(fd).Message())
	}

	sv, _ := dynamicValueOf(behavior.Child(s, fd))
	if r := mergeDynamic(dv, sv, nested); r != nil && (!live || r != dv) {
		storeDynamic(d.Mutable(fd).Message(), r)
	}
}

// mergeDynamicMessage applies msk to d, a google.protobuf.Struct, Value or
// ListValue message the destination holds, from s (nil when the source
// holds nothing).
func mergeDynamicMessage(d, s protoreflect.Message, msk fieldmask.FieldMask) {
	dv, live := dynamicValueOf(d)
	sv, _ := dynamicValueOf(s)

	if r := mergeDynamic(dv, sv, msk); r != nil && (!live || r != dv) {
		storeDynamic(d, r)
	}
}

// mergeDynamic returns the destination value dv (nil when absent) with sub
// applied from the source value sv (nil when absent): sv itself for a nil
// sub, dv updated in place, or a new Struct value replacing dv.
func mergeDynamic(dv, sv *structpb.Value, sub fieldmask.FieldMask) *structpb.Value {
	if sub == nil {
		return sv
	}

	if w, ok := sub[maskpath.Wildcard]; ok && (sv.GetListValue() != nil || (sv.GetStructValue() == nil && dv.GetListValue() != nil)) {
		mergeDynamicList(dv.GetListValue(), sv.GetListValue(), w)
		return dv
	}

	ss, ds := sv.GetStructValue(), dv.GetStructValue()

	switch {
	case ds != nil:
		mergeDynamicStruct(ds, ss, sub)
		return dv
	case ss == nil:
		return dv
	}

	created := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if mergeDynamicStruct(created, ss, sub); len(created.GetFields()) == 0 {
		return dv
	}

	return structpb.NewStructValue(created)
}

// mergeDynamicStruct applies msk to the entries of ds from ss (nil when the
// source holds no Struct there): every key msk names, then with "*" every
// other key of ss.
func mergeDynamicStruct(ds, ss *structpb.Struct, msk fieldmask.FieldMask) {
	set := func(key string, v *structpb.Value) {
		switch {
		case v != nil:
			if ds.Fields == nil {
				ds.Fields = map[string]*structpb.Value{}
			}

			ds.Fields[key] = v
		default:
			delete(ds.Fields, key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(msk)) {
		if key != maskpath.Wildcard {
			set(key, mergeDynamic(ds.GetFields()[key], ss.GetFields()[key], msk[key]))
		}
	}

	w, ok := msk[maskpath.Wildcard]
	if !ok {
		return
	}

	for _, key := range slices.Sorted(maps.Keys(ss.GetFields())) {
		if _, named := maskwalk.KeyMask(msk, key); !named {
			set(key, mergeDynamic(ds.GetFields()[key], ss.GetFields()[key], w))
		}
	}
}

// mergeDynamicList applies w, the "*" sub-mask of a list value, to every
// element of dl from the sl element of the same index (none when sl is nil
// or empty, which [checkDynamicCounts] allows).
func mergeDynamicList(dl, sl *structpb.ListValue, w fieldmask.FieldMask) {
	for i, e := range dl.GetValues() {
		var se *structpb.Value
		if i < len(sl.GetValues()) {
			se = sl.GetValues()[i]
		}

		if r := mergeDynamic(e, se, w); r != nil {
			dl.Values[i] = r
		}
	}
}

// checkDynamicCounts reports the first list value a "*" sub-path of sub
// updates in place whose source sv holds a number of elements other than
// zero and the destination dv's; either may be nil. path is the concrete
// path of the values.
func checkDynamicCounts(dv, sv *structpb.Value, sub fieldmask.FieldMask, path string) *elementCountError {
	if sub == nil {
		return nil
	}

	if w, ok := sub[maskpath.Wildcard]; ok && sv.GetListValue() != nil {
		sl, dl := sv.GetListValue().GetValues(), dv.GetListValue().GetValues()

		switch {
		case len(sl) == 0:
			return nil
		case dv.GetListValue() == nil || len(sl) != len(dl):
			return &elementCountError{Path: path, Reason: fmt.Sprintf(
				"the request holds %d elements and the stored resource %d; a %q sub-path updates elements in place by index",
				len(sl), len(dl), maskpath.Wildcard)}
		}

		for i := range dl {
			if err := checkDynamicCounts(dl[i], sl[i], w, behavior.JoinPath(path, strconv.Itoa(i))); err != nil {
				return err
			}
		}

		return nil
	}

	ss, ds := sv.GetStructValue(), dv.GetStructValue()
	if ss == nil {
		return nil
	}

	check := func(key string, s fieldmask.FieldMask) *elementCountError {
		return checkDynamicCounts(ds.GetFields()[key], ss.GetFields()[key], s, behavior.JoinPath(path, behavior.QuoteSegment(key)))
	}

	for _, key := range slices.Sorted(maps.Keys(sub)) {
		if key == maskpath.Wildcard {
			continue
		}

		if err := check(key, sub[key]); err != nil {
			return err
		}
	}

	if w, ok := sub[maskpath.Wildcard]; ok {
		for _, key := range slices.Sorted(maps.Keys(ss.GetFields())) {
			if _, named := maskwalk.KeyMask(sub, key); named {
				continue
			}

			if err := check(key, w); err != nil {
				return err
			}
		}
	}

	return nil
}

// dynamicValueOf returns m, a google.protobuf.Struct, Value or ListValue
// message, as a Value (nil for a nil or invalid m) and whether the Value
// shares the memory of m: it does for the generated types, while another
// implementation (dynamicpb) is copied.
func dynamicValueOf(m protoreflect.Message) (*structpb.Value, bool) {
	if m == nil || !m.IsValid() {
		return nil, false
	}

	switch v := m.Interface().(type) {
	case *structpb.Struct:
		return structpb.NewStructValue(v), true
	case *structpb.ListValue:
		return structpb.NewListValue(v), true
	case *structpb.Value:
		return v, true
	}

	out := newDynamic(m.Descriptor().FullName())
	if b, err := proto.Marshal(m.Interface()); err != nil || proto.Unmarshal(b, out) != nil {
		return nil, false
	}

	v, _ := dynamicValueOf(out.ProtoReflect())

	return v, false
}

// newDynamic returns an empty generated message of the dynamic well-known
// type name.
func newDynamic(name protoreflect.FullName) proto.Message {
	switch name {
	case maskpath.StructFullName:
		return &structpb.Struct{}
	case maskpath.ListValueFullName:
		return &structpb.ListValue{}
	default:
		return &structpb.Value{}
	}
}

// storeDynamic writes v into m, a google.protobuf.Struct, Value or ListValue
// message, replacing its content.
func storeDynamic(m protoreflect.Message, v *structpb.Value) {
	var want proto.Message

	switch m.Descriptor().FullName() {
	case maskpath.StructFullName:
		want = v.GetStructValue()
	case maskpath.ListValueFullName:
		want = v.GetListValue()
	default:
		want = v
	}

	target := m.Interface()
	if target == want {
		return
	}

	proto.Reset(target)

	if b, err := proto.Marshal(want); err == nil {
		_ = proto.Unmarshal(b, target) //nolint:errcheck // bytes of the same message type always decode.
	}
}

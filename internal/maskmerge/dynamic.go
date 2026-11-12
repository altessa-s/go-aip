// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// Dynamic JSON is read through each message's own descriptor. In particular,
// an unselected subtree never needs a conversion to the generated structpb
// representation (whose wire decoder imposes a payload recursion limit).
// All writes below target private messages owned by the enclosing merge.
func mergeDynamicField[M ~map[string]M](d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested M) error {
	result, err := mergeDynamic(behavior.Child(d, fd), behavior.Child(s, fd), nested)
	if err != nil || result == nil {
		return err
	}

	return SetValue(d, fd, protoreflect.ValueOfMessage(result))
}

func mergeDynamicMessage[M ~map[string]M](d, s protoreflect.Message, msk M) error {
	result, err := mergeDynamic(d, s, msk)
	if err != nil || result == nil || result == d {
		return err
	}

	return ReplaceMessage(d, result)
}

// mergeDynamic returns d updated by sub, a new object when a source object
// replaces a non-object, or s for a whole selection. A nil message is absent;
// copying a returned source into its destination can fail and must be checked.
func mergeDynamic[M ~map[string]M](d, s protoreflect.Message, sub M) (protoreflect.Message, error) {
	if sub == nil {
		return s, nil
	}

	dl, sl := dynamicList(d), dynamicList(s)
	ds, ss := dynamicStruct(d), dynamicStruct(s)

	if w, ok := sub[maskpath.Wildcard]; ok && (sl != nil || (ss == nil && dl != nil)) {
		return d, mergeDynamicList(dl, sl, w)
	}

	switch {
	case ds != nil:
		return d, mergeDynamicStruct(ds, ss, sub)
	case ss == nil:
		return d, nil
	}

	// The destination determines the descriptor and representation of a new
	// object. When absent, its caller converts the source-shaped result.
	template := d
	if template == nil {
		template = s
	}

	created := template.New()

	object := created
	if created.Descriptor().FullName() == maskpath.ValueFullName {
		object = created.Mutable(created.Descriptor().Fields().ByName("struct_value")).Message()
	}

	if err := mergeDynamicStruct(object, ss, sub); err != nil {
		return nil, err
	}

	if dynamicFields(object).Len() == 0 {
		return d, nil
	}

	return created, nil
}

func mergeDynamicStruct[M ~map[string]M](d, s protoreflect.Message, msk M) error {
	fd := d.Descriptor().Fields().ByName("fields")
	dm, sm := d.Mutable(fd).Map(), dynamicFields(s)
	set := func(key string, sub M) error {
		k := protoreflect.ValueOfString(key).MapKey()

		result, err := mergeDynamic(dynamicEntry(dm, k), dynamicEntry(sm, k), sub)
		if err != nil {
			return err
		}

		if result == nil {
			dm.Clear(k)
			return nil
		}

		value, err := copySingular(dm.NewValue(), protoreflect.ValueOfMessage(result), fd.MapValue())
		if err != nil {
			return err
		}

		dm.Set(k, value)

		return nil
	}

	for _, key := range slices.Sorted(maps.Keys(msk)) {
		if key != maskpath.Wildcard {
			if err := set(maskpath.JSONKey(key), msk[key]); err != nil {
				return err
			}
		}
	}

	if w, ok := msk[maskpath.Wildcard]; ok && sm != nil {
		for _, key := range behavior.SortedMapKeys(sm) {
			if _, named := maskwalk.KeyMask(msk, key.String()); !named {
				if err := set(key.String(), w); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func mergeDynamicList[M ~map[string]M](d, s protoreflect.Message, sub M) error {
	if d == nil {
		return nil
	}

	fd := d.Descriptor().Fields().ByName("values")
	dl, sl := d.Mutable(fd).List(), dynamicValues(s)

	for i := range dl.Len() {
		var source protoreflect.Message
		if i < ListLen(sl) {
			source = sl.Get(i).Message()
		}

		result, err := mergeDynamic(dl.Get(i).Message(), source, sub)
		if err != nil {
			return err
		}

		if result != nil {
			value, err := copySingular(dl.NewElement(), protoreflect.ValueOfMessage(result), fd)
			if err != nil {
				return err
			}

			dl.Set(i, value)
		}
	}

	return nil
}

// checkDynamicCounts reads only the selected JSON branches. There is no
// representation conversion whose failure could be mistaken for absence.
func checkDynamicCounts[M ~map[string]M](d, s protoreflect.Message, sub M, path string) *ElementCountError {
	if sub == nil {
		return nil
	}

	if w, ok := sub[maskpath.Wildcard]; ok && dynamicList(s) != nil {
		sl, dl := dynamicValues(dynamicList(s)), dynamicValues(dynamicList(d))

		switch {
		case ListLen(sl) == 0:
			return nil
		case dl == nil || sl.Len() != dl.Len():
			return &ElementCountError{Path: path, Reason: fmt.Sprintf(
				"the request holds %d elements and the stored resource %d; a %q sub-path updates elements in place by index",
				sl.Len(), ListLen(dl), maskpath.Wildcard)}
		}

		for i := range dl.Len() {
			if err := checkDynamicCounts(dl.Get(i).Message(), sl.Get(i).Message(), w, behavior.JoinPath(path, strconv.Itoa(i))); err != nil {
				return err
			}
		}

		return nil
	}

	sm, dm := dynamicFields(dynamicStruct(s)), dynamicFields(dynamicStruct(d))
	if sm == nil {
		return nil
	}

	check := func(key string, nested M) *ElementCountError {
		k := protoreflect.ValueOfString(key).MapKey()
		return checkDynamicCounts(dynamicEntry(dm, k), dynamicEntry(sm, k), nested, behavior.JoinPath(path, maskpath.QuoteJSONKey(key)))
	}

	for _, key := range slices.Sorted(maps.Keys(sub)) {
		if key != maskpath.Wildcard {
			if err := check(maskpath.JSONKey(key), sub[key]); err != nil {
				return err
			}
		}
	}

	if w, ok := sub[maskpath.Wildcard]; ok {
		for _, key := range behavior.SortedMapKeys(sm) {
			if _, named := maskwalk.KeyMask(sub, key.String()); !named {
				if err := check(key.String(), w); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func dynamicStruct(m protoreflect.Message) protoreflect.Message {
	return dynamicChild(m, maskpath.StructFullName, "struct_value")
}

func dynamicList(m protoreflect.Message) protoreflect.Message {
	return dynamicChild(m, maskpath.ListValueFullName, "list_value")
}

func dynamicChild(m protoreflect.Message, name protoreflect.FullName, field protoreflect.Name) protoreflect.Message {
	if m == nil || !m.IsValid() {
		return nil
	}

	if m.Descriptor().FullName() == name {
		return m
	}

	if m.Descriptor().FullName() == maskpath.ValueFullName {
		return behavior.Child(m, m.Descriptor().Fields().ByName(field))
	}

	return nil
}

func dynamicFields(m protoreflect.Message) protoreflect.Map {
	if m == nil {
		return nil
	}

	return m.Get(m.Descriptor().Fields().ByName("fields")).Map()
}

func dynamicValues(m protoreflect.Message) protoreflect.List {
	if m == nil {
		return nil
	}

	return m.Get(m.Descriptor().Fields().ByName("values")).List()
}

func dynamicEntry(m protoreflect.Map, key protoreflect.MapKey) protoreflect.Message {
	if m == nil || !m.Has(key) {
		return nil
	}

	return m.Get(key).Message()
}

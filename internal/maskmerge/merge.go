// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// ErrElementCountMismatch reports incompatible list lengths for an in-place merge.
var ErrElementCountMismatch = errors.New("fieldmask: element count differs from the stored list")

// CopyField sets fd of d to its value in s, a private copy of the source,
// or clears it when s is nil or does not populate it.
func CopyField(d, s protoreflect.Message, fd protoreflect.FieldDescriptor) error {
	if s != nil && s.Has(fd) {
		return SetValue(d, fd, s.Get(fd))
	}

	d.Clear(fd)

	return nil
}

// Tree applies msk, a resolved mask of md (trailing "*" dropped), from
// s to d (see [MergePaths]); s is a private copy of the source, nil where the
// source holds nothing. Element counts are checked first
// ([CheckElementCounts]); conversion failures are returned. Callers publish d
// only after the complete tree has succeeded.
func Tree[M ~map[string]M](d, s protoreflect.Message, msk M, md protoreflect.MessageDescriptor) error {
	if maskpath.IsDynamicMessage(md) {
		if wholeDynamic(msk) {
			fields := md.Fields()
			for i := range fields.Len() {
				if err := CopyField(d, s, fields.Get(i)); err != nil {
					return err
				}
			}

			return nil
		}

		return mergeDynamicMessage(d, s, msk)
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		var err error

		nested, ok := msk[string(fd.Name())]
		switch {
		case !ok:
		case nested == nil:
			err = CopyField(d, s, fd)
		case fd.IsList():
			err = mergeList(d, s, fd, nested[maskpath.Wildcard])
		case fd.IsMap():
			err = mergeMap(d, s, fd, nested)
		case maskpath.IsDynamicField(fd):
			err = mergeDynamicField(d, s, fd, nested)
		default:
			next := behavior.Child(s, fd)
			if next == nil && !d.Has(fd) {
				continue
			}

			err = Tree(d.Mutable(fd).Message(), next, nested, fd.Message())
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// mergeList applies sub, the "*" sub-mask of the repeated field fd, to every
// element of d from the s element of the same index (none when s holds
// fewer, which [CheckElementCounts] allows only for an empty source).
func mergeList[M ~map[string]M](d, s protoreflect.Message, fd protoreflect.FieldDescriptor, sub M) error {
	if sub == nil || !d.Has(fd) {
		return nil
	}

	var src protoreflect.List
	if s != nil && s.Has(fd) {
		src = s.Get(fd).List()
	}

	dst := d.Mutable(fd).List()
	for i := range dst.Len() {
		var elem protoreflect.Message
		if src != nil && i < src.Len() {
			elem = src.Get(i).Message()
		}

		if err := Tree(dst.Get(i).Message(), elem, sub, fd.Message()); err != nil {
			return err
		}
	}

	return nil
}

// mergeMap applies nested, the sub-mask of the map field fd, from s to d: a
// key named whole replaces or deletes its entry, a key with a sub-mask
// updates its entry in place, and the "*" sub-mask updates every entry of s
// no other key names.
func mergeMap[M ~map[string]M](d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested M) error {
	var src protoreflect.Map
	if s != nil && s.Has(fd) {
		src = s.Get(fd).Map()
	}

	kind := fd.MapKey().Kind()

	for _, key := range slices.Sorted(maps.Keys(nested)) {
		if key == maskpath.Wildcard {
			continue
		}

		if k, ok := maskpath.ParseMapKey(kind, key); ok {
			if err := mergeEntry(d, src, fd, k, nested[key]); err != nil {
				return err
			}
		}
	}

	wildcard := nested[maskpath.Wildcard]
	if wildcard == nil || src == nil {
		return nil
	}

	for _, k := range behavior.SortedMapKeys(src) {
		if _, named := maskwalk.KeyMask(nested, k.String()); !named {
			if err := mergeEntry(d, src, fd, k, wildcard); err != nil {
				return err
			}
		}
	}

	return nil
}

// mergeEntry applies sub to the entry k of the map field fd of d from src
// (nil when the source holds no map): with a nil sub the entry is replaced,
// or deleted when src lacks it; otherwise it is updated in place, created
// only when src holds it.
func mergeEntry[M ~map[string]M](d protoreflect.Message, src protoreflect.Map, fd protoreflect.FieldDescriptor, k protoreflect.MapKey, sub M) error {
	// An ignored keyed selection excludes the key without materializing it.
	if sub != nil && len(sub) == 0 {
		return nil
	}

	var sv protoreflect.Value
	if src != nil {
		sv = src.Get(k)
	}

	if sub == nil {
		switch {
		case sv.IsValid():
			value, err := copySingular(d.NewField(fd).Map().NewValue(), sv, fd.MapValue())
			if err != nil {
				return err
			}

			d.Mutable(fd).Map().Set(k, value)
		case d.Has(fd):
			d.Mutable(fd).Map().Clear(k)
		}

		return nil
	}

	var elem protoreflect.Message
	if sv.IsValid() {
		elem = sv.Message()
	}

	if elem == nil && (!d.Has(fd) || !d.Get(fd).Map().Has(k)) {
		return nil
	}

	return Tree(d.Mutable(fd).Map().Mutable(k).Message(), elem, sub, fd.MapValue().Message())
}

// ElementCountError is the [ErrElementCountMismatch] refusal of the list at
// Path.
type ElementCountError struct {
	Path   string
	Reason string
}

// Error describes the mismatched collection.
func (e *ElementCountError) Error() string {
	return fmt.Sprintf("fieldmask: path %q: %s", e.Path, e.Reason)
}

// Unwrap preserves the public element-count sentinel.
func (e *ElementCountError) Unwrap() error { return ErrElementCountMismatch }

// CheckElementCounts reports the first list a "*" sub-path of msk, a
// resolved mask of md, updates in place whose source s holds a number of
// elements other than zero and the destination d's (see [MergePaths]);
// either message may be nil (holding nothing). prefix is the concrete path
// of the messages.
func CheckElementCounts[M ~map[string]M](d, s protoreflect.Message, msk M, md protoreflect.MessageDescriptor, prefix string) *ElementCountError {
	if maskpath.IsDynamicMessage(md) {
		if wholeDynamic(msk) {
			return nil
		}

		return checkDynamicCounts(d, s, msk, prefix)
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		nested := msk[string(fd.Name())]
		if nested == nil {
			continue
		}

		path := behavior.JoinPath(prefix, string(fd.Name()))

		var err *ElementCountError

		switch {
		case fd.IsList():
			err = checkListCounts(d, s, fd, nested[maskpath.Wildcard], path)
		case fd.IsMap():
			err = checkMapCounts(d, s, fd, nested, path)
		default:
			err = CheckElementCounts(behavior.Child(d, fd), behavior.Child(s, fd), nested, fd.Message(), path)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// checkListCounts is [CheckElementCounts] for the repeated field fd at path
// with the "*" sub-mask sub.
func checkListCounts[M ~map[string]M](d, s protoreflect.Message, fd protoreflect.FieldDescriptor, sub M, path string) *ElementCountError {
	if sub == nil {
		return nil
	}

	dl, sl := ListOf(d, fd), ListOf(s, fd)

	switch n, m := ListLen(sl), ListLen(dl); {
	case n == 0:
		return nil
	case n != m:
		return &ElementCountError{Path: path, Reason: fmt.Sprintf(
			"the request holds %d elements and the stored resource %d; a %q sub-path updates elements in place by index",
			n, m, maskpath.Wildcard)}
	}

	for i := range dl.Len() {
		if err := CheckElementCounts(dl.Get(i).Message(), sl.Get(i).Message(), sub, fd.Message(), behavior.JoinPath(path, strconv.Itoa(i))); err != nil {
			return err
		}
	}

	return nil
}

// checkMapCounts is [CheckElementCounts] for the map field fd at path with
// the sub-mask nested: the entries each key with a sub-mask addresses.
func checkMapCounts[M ~map[string]M](d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested M, path string) *ElementCountError {
	if fd.MapValue().Message() == nil {
		return nil
	}

	dm, sm := MapOf(d, fd), MapOf(s, fd)
	entry := func(m protoreflect.Map, k protoreflect.MapKey) protoreflect.Message {
		if m == nil {
			return nil
		}

		if v := m.Get(k); v.IsValid() {
			return v.Message()
		}

		return nil
	}

	var keys []protoreflect.MapKey

	for _, key := range slices.Sorted(maps.Keys(nested)) {
		if nested[key] == nil || key == maskpath.Wildcard {
			continue
		}

		if k, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), key); ok {
			keys = append(keys, k)
		}
	}

	if nested[maskpath.Wildcard] != nil && sm != nil {
		for _, k := range behavior.SortedMapKeys(sm) {
			if _, named := maskwalk.KeyMask(nested, k.String()); !named {
				keys = append(keys, k)
			}
		}
	}

	for _, k := range keys {
		sub, ok := maskwalk.KeyMask(nested, k.String())
		if !ok {
			sub = nested[maskpath.Wildcard]
		}

		if err := CheckElementCounts(entry(dm, k), entry(sm, k), sub, fd.MapValue().Message(),
			behavior.JoinPath(path, behavior.QuoteSegment(k.String()))); err != nil {
			return err
		}
	}

	return nil
}

// ListOf returns the repeated field fd of m, nil when m is nil.
func ListOf(m protoreflect.Message, fd protoreflect.FieldDescriptor) protoreflect.List {
	if m == nil {
		return nil
	}

	return m.Get(fd).List()
}

// ListLen returns the length of l, zero for nil.
func ListLen(l protoreflect.List) int {
	if l == nil {
		return 0
	}

	return l.Len()
}

// MapOf returns the map field fd of m, nil when m is nil or does not hold
// it.
func MapOf(m protoreflect.Message, fd protoreflect.FieldDescriptor) protoreflect.Map {
	if m == nil || !m.Has(fd) {
		return nil
	}

	return m.Get(fd).Map()
}

// wholeDynamic recognizes the whole-root selection. Nested trailing wildcards
// are collapsed by path resolution before reaching Tree, so they still replace
// their containing field/key rather than becoming a root selection.
func wholeDynamic[M ~map[string]M](msk M) bool {
	sub, ok := msk[maskpath.Wildcard]
	return len(msk) == 1 && ok && sub == nil
}

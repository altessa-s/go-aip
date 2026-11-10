// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// ErrMessageMismatch is returned by [Merge] for messages that are
// nil (or typed-nil), not of one Go type or not of one descriptor.
var ErrMessageMismatch = errors.New("fieldmask: messages are nil or of different types")

// ErrElementCountMismatch reports a "*" sub-path of a repeated field
// ("aliases.*.display_name") whose source list holds a different number of
// elements than the destination list: the elements are paired by index, so
// the update cannot tell which element a value belongs to.
var ErrElementCountMismatch = errors.New("fieldmask: element count differs from the stored list")

// Merge copies the fields msk selects from src to dst, both messages of the
// same type, as [MergePaths] copies them: msk may name top-level fields,
// nested fields, map keys and "*" on repeated and map fields, in any spelling
// of its field segments. A selected field src does not populate is cleared
// in dst, fields outside the mask are kept, and dst never shares memory with
// src.
//
// The mask means exactly what it names. An empty mask changes nothing; for
// the fields an update implies when its mask is omitted, build the mask from
// src first:
//
//	err := fieldmask.FromSetFields[fieldmask.FieldMask](src, fieldmask.WithSkipFields("id")).Merge(dst, src)
//
// "*" alone ([fieldmask.FieldMask.SelectsAll]) replaces every top-level field and "*"
// with other paths is [fieldmask.ErrWildcardNotAlone]. A path that is not an update
// path ([ResolvePath] under the options, of which
// [WithStrictDynamicPaths] and the limits apply) is a *[fieldmask.ValidationError].
// Nil (including typed-nil) messages, or messages not of one Go type and one
// descriptor (a dynamicpb message and the generated type of its descriptor do
// not mix), are [ErrMessageMismatch]. Every check runs before the first
// write, so dst is changed all-or-nothing.
func Merge(msk fieldmask.FieldMask, dst, src proto.Message, opts ...Option) error {
	d, s, err := reflectPair(dst, src)
	if err != nil {
		return err
	}

	all, err := msk.SelectsAll()
	if err != nil {
		return err
	}

	if !all {
		return mergePaths(dst, src, msk.ToPaths(), newOptions(opts...))
	}

	fields := d.Descriptor().Fields()
	c := proto.Clone(s.Interface()).ProtoReflect()

	for i := range fields.Len() {
		copyField(d, c, fields.Get(i))
	}

	return nil
}

// MergePaths copies the fields paths name from src to dst, messages of one
// type: the merge of an [Plan]. Each path is an update path
// ([ResolvePath]: declared, JSON or snake_case field segments, map keys
// and "*" on repeated and map fields):
//   - a field path replaces the whole field (a parent path replaces the
//     block, a collection path or a trailing "*" the whole collection), and a
//     nested path only that field, keeping its siblings;
//   - a map key path ("labels.k") replaces that entry, or deletes it when src
//     does not hold it; a key of a google.protobuf.Struct ("attrs.color") is
//     such a map key;
//   - by default (not with [WithStrictDynamicPaths]) a path goes on into the
//     dynamic JSON below a Struct key, a google.protobuf.Value or through
//     "*" of a ListValue: a nested key ("attrs.work.from") is replaced from
//     src or deleted when src lacks it, "*" over a Struct value addresses
//     every key src holds, and a missing or non-Struct value on the way is
//     replaced by a new Struct only when src holds a value to write there;
//     "*" on a list value ("attrs.items.*.name") updates the elements in
//     place by index under the count rule of a repeated field below;
//   - a sub-path through a map key ("labels.k.display_name") updates that
//     entry in place, creating it when only src holds it; through "*" on a
//     map ("labels.*.display_name") it updates every entry src holds that no
//     other path names by key, and leaves the entries only dst holds alone;
//   - a sub-path through "*" on a repeated field ("aliases.*.display_name")
//     updates every element of dst in place from the src element of the same
//     index; src must hold as many elements as dst, or none (the field is
//     then cleared in every element), else the merge is refused with
//     [ErrElementCountMismatch].
//
// A named field src does not populate is cleared in dst; a nested path whose
// parent is set in neither message changes nothing (no empty parent or entry
// is created). Values are deep copies, so dst never shares memory with src.
// Every path and element count is checked before the first write
// (*[fieldmask.ValidationError] for a bad path, [ErrMessageMismatch] for nil or mixed
// messages, [ErrElementCountMismatch]), so dst is changed all-or-nothing; no
// paths change nothing. Fields are written in declaration order and map keys
// in sorted order, so two paths setting members of one oneof leave the later
// declared member. The mask limits of the options ([WithMaxPaths],
// [WithMaxPathLength], [WithMaxPathDepth]) are checked before anything else.
func MergePaths(dst, src proto.Message, paths []string, opts ...Option) error {
	return mergePaths(dst, src, paths, newOptions(opts...))
}

func mergePaths(dst, src proto.Message, paths []string, policy *options) error {
	d, s, err := reflectPair(dst, src)
	if err != nil {
		return err
	}

	if err := policy.validateMaskLimits(paths); err != nil {
		return err
	}

	md := d.Descriptor()
	tree := fieldmask.FieldMask{}

	for _, p := range paths {
		segs, reason := resolveUpdateSegments(md, p, policy)
		if reason != "" {
			return &fieldmask.ValidationError{Path: p, Reason: "not an update path of " + string(md.FullName()) + ": " + reason}
		}

		maskwalk.Insert(tree, segs)
	}

	if len(tree) == 0 {
		return nil
	}

	c := proto.Clone(s.Interface()).ProtoReflect()
	if err := checkElementCounts(d, c, tree, md, ""); err != nil {
		return err
	}

	mergeTree(d, c, tree, md)

	return nil
}

// reflectPair returns the reflected dst and src, which must be valid
// (non-nil, not typed-nil) messages of the same Go type and the same
// descriptor. Equal full names are not enough (independently built
// descriptors cannot be mixed), and neither is a shared descriptor: a
// dynamicpb message and the generated type of its descriptor hold nested
// values of different Go types, which the generated setters reject with a
// panic. With one Go type and one descriptor every value of src is valid in
// dst, so the copy cannot fail once the paths are resolved. Converting
// values through a marshal round trip was not chosen: it costs a full
// encode/decode per field and changes unknown-field handling.
func reflectPair(dst, src proto.Message) (protoreflect.Message, protoreflect.Message, error) {
	if dst == nil || src == nil {
		return nil, nil, ErrMessageMismatch
	}

	d, s := dst.ProtoReflect(), src.ProtoReflect()
	if !d.IsValid() || !s.IsValid() {
		return nil, nil, fmt.Errorf("%w: nil message", ErrMessageMismatch)
	}

	if reflect.TypeOf(dst) != reflect.TypeOf(src) || d.Descriptor() != s.Descriptor() {
		return nil, nil, fmt.Errorf("%w: %T (%s) and %T (%s)", ErrMessageMismatch,
			dst, d.Descriptor().FullName(), src, s.Descriptor().FullName())
	}

	return d, s, nil
}

// copyField sets fd of d to its value in s, a private copy of the source,
// or clears it when s is nil or does not populate it.
func copyField(d, s protoreflect.Message, fd protoreflect.FieldDescriptor) {
	if s != nil && s.Has(fd) {
		d.Set(fd, s.Get(fd))
		return
	}

	d.Clear(fd)
}

// mergeTree applies msk, a resolved mask of md (trailing "*" dropped), from
// s to d (see [MergePaths]); s is a private copy of the source, nil where the
// source holds nothing. Element counts are checked first
// ([checkElementCounts]), so nothing here fails.
func mergeTree(d, s protoreflect.Message, msk fieldmask.FieldMask, md protoreflect.MessageDescriptor) {
	if maskpath.IsDynamicMessage(md) {
		mergeDynamicMessage(d, s, msk)
		return
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		nested, ok := msk[string(fd.Name())]
		switch {
		case !ok:
		case nested == nil:
			copyField(d, s, fd)
		case fd.IsList():
			mergeList(d, s, fd, nested[maskpath.Wildcard])
		case fd.IsMap():
			mergeMap(d, s, fd, nested)
		case maskpath.IsDynamicField(fd):
			mergeDynamicField(d, s, fd, nested)
		default:
			next := behavior.Child(s, fd)
			if next == nil && !d.Has(fd) {
				continue
			}

			mergeTree(d.Mutable(fd).Message(), next, nested, fd.Message())
		}
	}
}

// mergeList applies sub, the "*" sub-mask of the repeated field fd, to every
// element of d from the s element of the same index (none when s holds
// fewer, which [checkElementCounts] allows only for an empty source).
func mergeList(d, s protoreflect.Message, fd protoreflect.FieldDescriptor, sub fieldmask.FieldMask) {
	if sub == nil || !d.Has(fd) {
		return
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

		mergeTree(dst.Get(i).Message(), elem, sub, fd.Message())
	}
}

// mergeMap applies nested, the sub-mask of the map field fd, from s to d: a
// key named whole replaces or deletes its entry, a key with a sub-mask
// updates its entry in place, and the "*" sub-mask updates every entry of s
// no other key names.
func mergeMap(d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask) {
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
			mergeEntry(d, src, fd, k, nested[key])
		}
	}

	wildcard := nested[maskpath.Wildcard]
	if wildcard == nil || src == nil {
		return
	}

	for _, k := range behavior.SortedMapKeys(src) {
		if _, named := maskwalk.KeyMask(nested, k.String()); !named {
			mergeEntry(d, src, fd, k, wildcard)
		}
	}
}

// mergeEntry applies sub to the entry k of the map field fd of d from src
// (nil when the source holds no map): with a nil sub the entry is replaced,
// or deleted when src lacks it; otherwise it is updated in place, created
// only when src holds it.
func mergeEntry(d protoreflect.Message, src protoreflect.Map, fd protoreflect.FieldDescriptor, k protoreflect.MapKey, sub fieldmask.FieldMask) {
	var sv protoreflect.Value
	if src != nil {
		sv = src.Get(k)
	}

	if sub == nil {
		switch {
		case sv.IsValid():
			d.Mutable(fd).Map().Set(k, sv)
		case d.Has(fd):
			d.Mutable(fd).Map().Clear(k)
		}

		return
	}

	var elem protoreflect.Message
	if sv.IsValid() {
		elem = sv.Message()
	}

	if elem == nil && (!d.Has(fd) || !d.Get(fd).Map().Has(k)) {
		return
	}

	mergeTree(d.Mutable(fd).Map().Mutable(k).Message(), elem, sub, fd.MapValue().Message())
}

// elementCountError is the [ErrElementCountMismatch] refusal of the list at
// Path.
type elementCountError struct {
	Path   string
	Reason string
}

func (e *elementCountError) Error() string {
	return fmt.Sprintf("fieldmask: path %q: %s", e.Path, e.Reason)
}

func (e *elementCountError) Unwrap() error { return ErrElementCountMismatch }

// checkElementCounts reports the first list a "*" sub-path of msk, a
// resolved mask of md, updates in place whose source s holds a number of
// elements other than zero and the destination d's (see [MergePaths]);
// either message may be nil (holding nothing). prefix is the concrete path
// of the messages.
func checkElementCounts(d, s protoreflect.Message, msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, prefix string) *elementCountError {
	if maskpath.IsDynamicMessage(md) {
		dv, _ := dynamicValueOf(d)
		sv, _ := dynamicValueOf(s)

		return checkDynamicCounts(dv, sv, msk, prefix)
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		nested := msk[string(fd.Name())]
		if nested == nil {
			continue
		}

		path := behavior.JoinPath(prefix, string(fd.Name()))

		var err *elementCountError

		switch {
		case fd.IsList():
			err = checkListCounts(d, s, fd, nested[maskpath.Wildcard], path)
		case fd.IsMap():
			err = checkMapCounts(d, s, fd, nested, path)
		default:
			err = checkElementCounts(behavior.Child(d, fd), behavior.Child(s, fd), nested, fd.Message(), path)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// checkListCounts is [checkElementCounts] for the repeated field fd at path
// with the "*" sub-mask sub.
func checkListCounts(d, s protoreflect.Message, fd protoreflect.FieldDescriptor, sub fieldmask.FieldMask, path string) *elementCountError {
	if sub == nil {
		return nil
	}

	dl, sl := listOf(d, fd), listOf(s, fd)

	switch n, m := listLen(sl), listLen(dl); {
	case n == 0:
		return nil
	case n != m:
		return &elementCountError{Path: path, Reason: fmt.Sprintf(
			"the request holds %d elements and the stored resource %d; a %q sub-path updates elements in place by index",
			n, m, maskpath.Wildcard)}
	}

	for i := range dl.Len() {
		if err := checkElementCounts(dl.Get(i).Message(), sl.Get(i).Message(), sub, fd.Message(), behavior.JoinPath(path, strconv.Itoa(i))); err != nil {
			return err
		}
	}

	return nil
}

// checkMapCounts is [checkElementCounts] for the map field fd at path with
// the sub-mask nested: the entries each key with a sub-mask addresses.
func checkMapCounts(d, s protoreflect.Message, fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, path string) *elementCountError {
	if fd.MapValue().Message() == nil {
		return nil
	}

	dm, sm := mapOf(d, fd), mapOf(s, fd)
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

		if err := checkElementCounts(entry(dm, k), entry(sm, k), sub, fd.MapValue().Message(),
			behavior.JoinPath(path, behavior.QuoteSegment(k.String()))); err != nil {
			return err
		}
	}

	return nil
}

// listOf returns the repeated field fd of m, nil when m is nil.
func listOf(m protoreflect.Message, fd protoreflect.FieldDescriptor) protoreflect.List {
	if m == nil {
		return nil
	}

	return m.Get(fd).List()
}

// listLen returns the length of l, zero for nil.
func listLen(l protoreflect.List) int {
	if l == nil {
		return 0
	}

	return l.Len()
}

// mapOf returns the map field fd of m, nil when m is nil or does not hold
// it.
func mapOf(m protoreflect.Message, fd protoreflect.FieldDescriptor) protoreflect.Map {
	if m == nil || !m.Has(fd) {
		return nil
	}

	return m.Get(fd).Map()
}

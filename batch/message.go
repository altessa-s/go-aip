// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

import (
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Declared names of the standard fields ValidateRequest reads.
const (
	nameField     protoreflect.Name = "name"
	parentField   protoreflect.Name = "parent"
	requestsField protoreflect.Name = "requests"
	namesField    protoreflect.Name = "names"
)

// ValidateRequest validates a batch request message of the standard AIP-231
// to AIP-235 shape by its descriptor, the checks in this order:
//
//  1. Size ([CheckSize]) of the repeated message field "requests" or, when
//     the message has none, the repeated string field "names". A message
//     with neither is [ErrNotBatchRequest].
//  2. Parent ([CheckParent]), when the request has a string "parent" field
//     that is set. The parent of an item is the "parent" field of a nested
//     request, else the parent of the resource name in its "name" field, else
//     the parent of the "name" of its first singular message field that has
//     one (the resource of an Update request); for "names" the parent of each
//     name. The parent of a resource name is the name without its last two
//     segments, and a name of fewer than three segments (a top-level resource
//     or a bare id) has none, so it is not checked.
//  3. Hoisted fields ([CheckHoisted]) of "requests": every other singular
//     field of the batch request that a nested request declares with the same
//     name, kind and message type (update_mask, validate_only, ...). When both
//     are set the values must be equal.
//  4. Duplicates ([CheckUnique]) with [WithUniqueKeys]: the names of
//     "names", or the "name" (or resource "name") of each nested request.
//
// Error paths use the JSON field names of the message.
func ValidateRequest(req proto.Message, opts ...Option) error {
	o := newValidateOptions(opts...)

	if req == nil {
		return ErrNotBatchRequest
	}

	m := req.ProtoReflect()
	md := m.Descriptor()

	if fd := md.Fields().ByName(requestsField); fd != nil && fd.IsList() && fd.Message() != nil {
		return o.validateRequests(m, fd)
	}

	if fd := md.Fields().ByName(namesField); fd != nil && fd.IsList() && fd.Kind() == protoreflect.StringKind {
		return o.validateNames(m, fd)
	}

	return ErrNotBatchRequest
}

// validateNames validates the "names" form.
func (o *validateOptions) validateNames(m protoreflect.Message, fd protoreflect.FieldDescriptor) error {
	field := fd.JSONName()

	names := stringList(m.Get(fd).List())
	if err := o.checkSize(field, len(names)); err != nil {
		return err
	}

	if err := CheckParent(field, names, topParent(m), func(n string) (string, string) { return parentOf(n), "" }); err != nil {
		return err
	}

	if o.uniqueKeys {
		return CheckUnique(field, names, func(n string) (string, string) { return n, "" })
	}

	return nil
}

// validateRequests validates the "requests" form.
func (o *validateOptions) validateRequests(m protoreflect.Message, fd protoreflect.FieldDescriptor) error {
	field := fd.JSONName()

	list := m.Get(fd).List()
	items := make([]protoreflect.Message, list.Len())

	for i := range items {
		items[i] = list.Get(i).Message()
	}

	if err := o.checkSize(field, len(items)); err != nil {
		return err
	}

	nested := fd.Message()

	if err := CheckParent(field, items, topParent(m), itemParent(nested)); err != nil {
		return err
	}

	if err := checkHoistedFields(m, fd, items); err != nil {
		return err
	}

	if !o.uniqueKeys {
		return nil
	}

	return CheckUnique(field, items, func(item protoreflect.Message) (string, string) {
		name, path, _ := itemName(item, nested)
		return name, path
	})
}

// checkHoistedFields applies [CheckHoisted] to every singular field of the
// batch request but "parent" and the repeated field that the nested request
// declares alike.
func checkHoistedFields(m protoreflect.Message, itemsFD protoreflect.FieldDescriptor, items []protoreflect.Message) error {
	fields := m.Descriptor().Fields()
	nested := itemsFD.Message()

	for i := range fields.Len() {
		top := fields.Get(i)
		if top == itemsFD || top.Name() == parentField || top.Cardinality() == protoreflect.Repeated || !m.Has(top) {
			continue
		}

		inner := nested.Fields().ByName(top.Name())
		if inner == nil || !sameType(top, inner) {
			continue
		}

		value := m.Get(top)

		for j, item := range items {
			if item.Has(inner) && !item.Get(inner).Equal(value) {
				return NewItemError(itemsFD.JSONName(), j,
					fieldError(inner.JSONName(), "does not match the value of the batch request", ErrHoistedMismatch))
			}
		}
	}

	return nil
}

// sameType reports whether a and b are singular fields of one kind and, for
// a message or an enum, of one type.
func sameType(a, b protoreflect.FieldDescriptor) bool {
	if a.Kind() != b.Kind() || b.Cardinality() == protoreflect.Repeated || b.IsMap() {
		return false
	}

	switch a.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return a.Message().FullName() == b.Message().FullName()
	case protoreflect.EnumKind:
		return a.Enum().FullName() == b.Enum().FullName()
	default:
		return true
	}
}

// itemParent returns the parent accessor of nested requests of type md: the
// "parent" field, else the parent of the item's resource name.
func itemParent(md protoreflect.MessageDescriptor) func(protoreflect.Message) (string, string) {
	if fd := md.Fields().ByName(parentField); fd != nil && isSingularString(fd) {
		return func(item protoreflect.Message) (string, string) { return item.Get(fd).String(), fd.JSONName() }
	}

	return func(item protoreflect.Message) (string, string) {
		name, path, ok := itemName(item, md)
		if !ok {
			return "", ""
		}

		return parentOf(name), path
	}
}

// itemName returns the resource name a nested request addresses and its
// JSON path: its "name" field, else the "name" field of its first singular
// message field that has one.
func itemName(item protoreflect.Message, md protoreflect.MessageDescriptor) (name, path string, ok bool) {
	if fd := md.Fields().ByName(nameField); fd != nil && isSingularString(fd) {
		return item.Get(fd).String(), fd.JSONName(), true
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || fd.Cardinality() == protoreflect.Repeated {
			continue
		}

		inner := fd.Message().Fields().ByName(nameField)
		if inner == nil || !isSingularString(inner) {
			continue
		}

		if !item.Has(fd) {
			return "", "", false
		}

		return item.Get(fd).Message().Get(inner).String(), fd.JSONName() + "." + inner.JSONName(), true
	}

	return "", "", false
}

// topParent returns the "parent" of the batch request, or "".
func topParent(m protoreflect.Message) string {
	fd := m.Descriptor().Fields().ByName(parentField)
	if fd == nil || !isSingularString(fd) {
		return ""
	}

	return m.Get(fd).String()
}

// parentOf returns the parent of the resource name: the name without its
// last two segments (collection and id), or "" for a name of fewer than
// three segments.
func parentOf(name string) string {
	end := strings.LastIndexByte(name, '/')
	if end <= 0 {
		return ""
	}

	end = strings.LastIndexByte(name[:end], '/')
	if end <= 0 {
		return ""
	}

	return name[:end]
}

func isSingularString(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.StringKind && fd.Cardinality() != protoreflect.Repeated
}

func stringList(list protoreflect.List) []string {
	out := make([]string, list.Len())
	for i := range out {
		out[i] = list.Get(i).String()
	}

	return out
}

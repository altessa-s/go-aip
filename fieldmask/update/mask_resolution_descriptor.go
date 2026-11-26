// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// normalizeResolvedPair checks only the dynamic messages reached by the
// validation selection or the original write effect.
func (u *updateCheck) normalizeResolvedPair(root msgPair, scope, effect fieldmask.FieldMask, implied bool) msgPair {
	if !isDynamicValue(root.req) && !isDynamicValue(root.cur) {
		return root
	}

	scope = scope.Clone()
	for _, path := range effect.ToPaths() {
		maskwalk.Insert(scope, maskpath.Split(path))
	}

	check := resolvedDescriptorCheck{update: u, compatible: make(map[resolvedDescriptorPair]bool)}
	if implied && u.verify {
		check.includeImpliedProtected(scope, root.req, 0)
	}

	root.req, _ = check.message(root.req, root.req.Descriptor(), scope, 0)
	if root.cur != nil && u.refusal == nil {
		root.cur, _ = check.message(root.cur, root.cur.Descriptor(), scope, 0)
	}

	return root
}

func isDynamicValue(m protoreflect.Message) bool {
	if m == nil {
		return false
	}

	_, dynamic := m.Interface().(*dynamicpb.Message)

	return dynamic
}

type resolvedDescriptorPair struct {
	actual, declared protoreflect.MessageDescriptor
}

type resolvedDescriptorCheck struct {
	update     *updateCheck
	compatible map[resolvedDescriptorPair]bool
}

// includeImpliedProtected adds populated message branches that the implied
// protected-input walk visits even if synthesis did not emit their paths.
func (c *resolvedDescriptorCheck) includeImpliedProtected(scope fieldmask.FieldMask, req protoreflect.Message, depth int) {
	if !isDynamicValue(req) || depth >= requiredMaxDepth || maskpath.IsDynamicMessage(req.Descriptor()) {
		return
	}

	fields := req.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Message() == nil || !req.Has(fd) {
			continue
		}

		b, skip := c.update.behaviorOf(fd, depth == 0)
		if skip || b == annotations.FieldBehavior_OUTPUT_ONLY {
			continue
		}

		name := string(fd.Name())
		if isImmutableBehavior(b) {
			scope[name] = nil
			continue
		}

		if !c.update.policy.expandable(fd) || !hasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, c.update.policy) {
			continue
		}

		sub, selected := scope[name]
		if selected && sub == nil {
			continue
		}

		if !selected {
			sub = fieldmask.FieldMask{}
			scope[name] = sub
		}

		c.includeImpliedProtected(sub, req.Get(fd).Message(), depth+1)
	}
}

// message rebuilds only ancestors of normalized children. The private
// reflection view shares unvisited values with the read-only input.
func (c *resolvedDescriptorCheck) message(m protoreflect.Message, declared protoreflect.MessageDescriptor,
	scope fieldmask.FieldMask, depth int,
) (protoreflect.Message, bool) {
	if m == nil || !m.IsValid() {
		return m, false
	}

	actual := m.Descriptor()

	foreign := actual != declared
	switch {
	case foreign && !compatibleResolvedDescriptor(actual, declared, c.compatible):
		c.update.refuse(fmt.Errorf("%w: incompatible descriptor for %s", ErrMessageMismatch, declared.FullName()))
		return m, false
	case !foreign && !isDynamicValue(m):
		return m, false
	case depth > requiredMaxDepth:
		c.update.refuse(fieldbehavior.ErrMaxDepthExceeded)
		return m, false
	}

	c.update.expandVisits++
	if !c.update.withinVisits("") {
		return m, false
	}

	var snapshot *dynamicpb.Message
	if foreign {
		snapshot = shallowResolvedMessage(m, declared)
	}

	fields := declared.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		source := actual.Fields().ByNumber(fd.Number())

		elem := maskpath.ValueMessage(fd)
		if elem == nil || !m.Has(source) {
			continue
		}

		sub, selected := scope[string(fd.Name())]
		b, skip := c.update.behaviorOf(fd, depth == 0)

		identifier := c.update.verify && b == annotations.FieldBehavior_IDENTIFIER
		if skip || b == annotations.FieldBehavior_OUTPUT_ONLY || (!selected && scope != nil && !identifier) {
			continue
		}

		value, changed := c.field(m.Get(source), fd, elem, sub, depth+1)
		if c.update.refusal != nil {
			return m, false
		}

		if changed {
			if snapshot == nil {
				snapshot = shallowResolvedMessage(m, declared)
			}

			snapshot.Set(fd, value)
		}
	}

	if snapshot != nil {
		return snapshot, true
	}

	return m, false
}

func (c *resolvedDescriptorCheck) field(value protoreflect.Value, fd protoreflect.FieldDescriptor,
	elem protoreflect.MessageDescriptor, scope fieldmask.FieldMask, depth int,
) (protoreflect.Value, bool) {
	switch {
	case fd.IsMap():
		return c.mapField(value.Map(), fd, elem, scope, depth)
	case fd.IsList():
		if scope != nil {
			sub, selected := scope[maskpath.Wildcard]
			if !selected {
				return value, false
			}

			scope = sub
		}

		list := value.List()

		var copyList protoreflect.List

		for i := range list.Len() {
			entry, changed := c.message(list.Get(i).Message(), elem, scope, depth)
			if c.update.refusal != nil {
				return value, false
			}

			if changed {
				if copyList == nil {
					copyList = copyResolvedCollection(fd, value).List()
				}

				copyList.Set(i, protoreflect.ValueOfMessage(entry))
			}
		}

		if copyList != nil {
			return protoreflect.ValueOfList(copyList), true
		}
	default:
		child, changed := c.message(value.Message(), elem, scope, depth)
		return protoreflect.ValueOfMessage(child), changed
	}

	return value, false
}

func (c *resolvedDescriptorCheck) mapField(values protoreflect.Map, fd protoreflect.FieldDescriptor,
	elem protoreflect.MessageDescriptor, scope fieldmask.FieldMask, depth int,
) (protoreflect.Value, bool) {
	var copyMap protoreflect.Map

	for _, key := range resolvedMapKeys(values, fd, scope) {
		sub, named := maskwalk.KeyMask(scope, key.String())
		if !named {
			sub = scope[maskpath.Wildcard]
		}

		entry, changed := c.message(values.Get(key).Message(), elem, sub, depth)
		if c.update.refusal != nil {
			return protoreflect.ValueOfMap(values), false
		}

		if changed {
			if copyMap == nil {
				copyMap = copyResolvedCollection(fd, protoreflect.ValueOfMap(values)).Map()
			}

			copyMap.Set(key, protoreflect.ValueOfMessage(entry))
		}
	}

	if copyMap != nil {
		return protoreflect.ValueOfMap(copyMap), true
	}

	return protoreflect.ValueOfMap(values), false
}

// resolvedMapKeys avoids scanning the request map for a named-key selection.
func resolvedMapKeys(values protoreflect.Map, fd protoreflect.FieldDescriptor, scope fieldmask.FieldMask) []protoreflect.MapKey {
	if _, wildcard := scope[maskpath.Wildcard]; scope == nil || wildcard {
		return behavior.SortedMapKeys(values)
	}

	keys := make([]protoreflect.MapKey, 0, len(scope))
	for name := range scope {
		key, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), maskpath.JSONKey(name))
		if ok && values.Has(key) {
			keys = append(keys, key)
		}
	}

	slices.SortFunc(keys, func(a, b protoreflect.MapKey) int { return strings.Compare(a.String(), b.String()) })

	return keys
}

func shallowResolvedMessage(m protoreflect.Message, declared protoreflect.MessageDescriptor) *dynamicpb.Message {
	snapshot := dynamicpb.NewMessage(declared)

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if !fd.IsExtension() {
			target := declared.Fields().ByNumber(fd.Number())
			if target != fd && (fd.IsList() || fd.IsMap()) {
				v = copyResolvedCollection(target, v)
			}

			fd = target
		}

		snapshot.Set(fd, v)

		return true
	})
	snapshot.SetUnknown(m.GetUnknown())

	return snapshot
}

func copyResolvedCollection(fd protoreflect.FieldDescriptor, v protoreflect.Value) protoreflect.Value {
	copyValue := dynamicpb.NewMessage(fd.ContainingMessage()).NewField(fd)
	if fd.IsMap() {
		dst := copyValue.Map()
		v.Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
			dst.Set(key, value)
			return true
		})
	} else {
		src, dst := v.List(), copyValue.List()
		for i := range src.Len() {
			dst.Append(src.Get(i))
		}
	}

	return copyValue
}

// Check the schema before wire conversion can turn incompatible fields into
// unknown data. The pair cache terminates recursive descriptor graphs.
func compatibleResolvedDescriptor(actual, declared protoreflect.MessageDescriptor, checked map[resolvedDescriptorPair]bool) bool {
	if actual == declared {
		return true
	}

	if declared == nil || actual.FullName() != declared.FullName() {
		return false
	}

	pair := resolvedDescriptorPair{actual: actual, declared: declared}
	if checked[pair] {
		return true
	}

	if !proto.Equal(protodesc.ToDescriptorProto(actual), protodesc.ToDescriptorProto(declared)) {
		return false
	}

	checked[pair] = true

	fields := actual.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		other := declared.Fields().ByNumber(fd.Number())

		if md := maskpath.ValueMessage(fd); md != nil && !compatibleResolvedDescriptor(md, maskpath.ValueMessage(other), checked) {
			return false
		}

		if fd.IsMap() {
			fd, other = fd.MapValue(), other.MapValue()
		}

		if enum := fd.Enum(); enum != nil && enum != other.Enum() &&
			(enum.FullName() != other.Enum().FullName() || enum.IsClosed() != other.Enum().IsClosed() ||
				!proto.Equal(protodesc.ToEnumDescriptorProto(enum), protodesc.ToEnumDescriptorProto(other.Enum()))) {
			return false
		}
	}

	return true
}

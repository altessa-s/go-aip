// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// ProtectedChanges is the single protected-update decision procedure. It merges
// selection into a private copy of stored using Tree, then compares every
// protected value, ignoring OUTPUT_ONLY descendants. Thus writes, clears,
// collection removals and oneof switches are all effects of the real merge.
// A selected merge owns normalized request and stored snapshots plus a private
// merge destination. Callers must evaluate
// the complete selection once, not repeat it for paths covered by that selection;
// copying costs a constant multiple of resource size per call, plus the traversal budget.
// selection must have the same resolved form as Tree; nil replaces the message
// whole, whereas a non-nil empty selection changes nothing. visit bounds the
// comparison walk; false stops it, leaving error reporting to the caller.
// Subtrees with no protected field under resolve are skipped before visit;
// their values cannot consume its depth or traversal budget. Relevance is
// cached only within this comparison and handles recursive descriptors.
// OUTPUT_ONLY paths are ignored before checking merge preconditions on the
// remaining selection; a failed precondition is returned, never treated as equality.
// When supplied, top[0] overrides behaviors at the resource root only; nested
// occurrences of the same message type still use resolve.
func ProtectedChanges[M ~map[string]M](request, stored protoreflect.Message, selection M, resolve behavior.Resolve,
	prefix string, visit func(string, int) bool, top ...behavior.Resolve,
) ([]behavior.Violation, error) {
	template := request
	if template == nil {
		template = stored
	}

	if template == nil {
		return nil, nil
	}

	clone := func(m protoreflect.Message) (protoreflect.Message, error) {
		if m == nil {
			return template.New(), nil
		}

		return convertMessage(m, m)
	}

	// Normalize children before either the precondition walk or comparison
	// uses a parent's descriptors. dynamicpb can hold a foreign child by name.
	before, err := clone(stored)
	if err != nil {
		return nil, err
	}

	input, err := clone(request)
	if err != nil {
		return nil, err
	}

	after := input

	if selection != nil {
		selection = cloneSelection(selection)
		keepInputFields(template.Descriptor(), selection, resolve)

		if err := CheckElementCounts(before, input, selection, template.Descriptor(), prefix); err != nil {
			return nil, err
		}

		after, err = clone(before)
		if err != nil {
			return nil, err
		}

		if err := Tree(after, input, selection, template.Descriptor()); err != nil {
			return nil, err
		}
	}

	c := comparison{resolve: resolve, visit: visit, relevant: map[protoreflect.MessageDescriptor]bool{}}
	if len(top) > 0 {
		c.top = top[0]
	}

	c.walk(after, before, prefix, 0)

	return c.violations, nil
}

// comparison walks the result of the merge, never the request selection. This
// is also used for whole replacements and implicit oneof sibling clears.
type comparison struct {
	resolve    behavior.Resolve
	top        behavior.Resolve
	visit      func(string, int) bool
	violations []behavior.Violation
	relevant   map[protoreflect.MessageDescriptor]bool
}

func (c *comparison) walk(after, stored protoreflect.Message, prefix string, depth int) {
	template := after
	if template == nil {
		template = stored
	}

	if template == nil {
		return
	}

	if maskpath.IsDynamicMessage(template.Descriptor()) {
		return // JSON keys are data, not annotated protobuf fields
	}

	if (depth != 0 || c.top == nil) && !c.hasProtected(template.Descriptor()) {
		return
	}

	if c.visit != nil && !c.visit(prefix, depth) {
		return
	}

	fields := template.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		resolve := c.resolve
		if depth == 0 && c.top != nil {
			resolve = c.top
		}

		b := Managed(resolve(fd))
		if b == annotations.FieldBehavior_OUTPUT_ONLY {
			continue
		}

		path := behavior.JoinPath(prefix, string(fd.Name()))

		if b == annotations.FieldBehavior_IMMUTABLE || b == annotations.FieldBehavior_IDENTIFIER {
			if !behavior.SameInputValue(after, stored, fd, c.resolve) {
				c.violations = append(c.violations, behavior.ProtectedViolation(path, b))
			}

			continue
		}

		if md := maskpath.ValueMessage(fd); md == nil || !c.hasProtected(md) {
			continue
		}

		if behavior.SameValue(after, stored, fd) {
			continue
		}

		switch {
		case fd.IsList():
			c.list(after, stored, fd, path, depth+1)
		case fd.IsMap():
			c.mapValues(after, stored, fd, path, depth+1)
		default:
			c.walk(behavior.Child(after, fd), behavior.Child(stored, fd), path, depth+1)
		}
	}
}

// hasProtected searches the schema under this call's resolver, not annotations
// alone. Cache only completed searches: a recursive back edge is not proof that
// a message cannot reach a protected field through another member of the cycle.
func (c *comparison) hasProtected(md protoreflect.MessageDescriptor) bool {
	if found, ok := c.relevant[md]; ok {
		return found
	}

	seen := map[protoreflect.MessageDescriptor]bool{}

	var search func(protoreflect.MessageDescriptor) bool

	search = func(md protoreflect.MessageDescriptor) bool {
		if md == nil || seen[md] || maskpath.IsDynamicMessage(md) {
			return false
		}

		seen[md] = true
		fields := md.Fields()

		for i := range fields.Len() {
			fd := fields.Get(i)
			switch Managed(c.resolve(fd)) {
			case annotations.FieldBehavior_OUTPUT_ONLY:
				continue
			case annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER:
				return true
			default:
				if search(maskpath.ValueMessage(fd)) {
					return true
				}
			}
		}

		return false
	}

	found := search(md)
	c.relevant[md] = found

	return found
}

func (c *comparison) list(after, stored protoreflect.Message, fd protoreflect.FieldDescriptor, path string, depth int) {
	a, b := ListOf(after, fd), ListOf(stored, fd)
	for i := range max(ListLen(a), ListLen(b)) {
		var av, bv protoreflect.Message
		if i < ListLen(a) {
			av = a.Get(i).Message()
		}

		if i < ListLen(b) {
			bv = b.Get(i).Message()
		}

		c.walk(av, bv, behavior.JoinPath(path, strconv.Itoa(i)), depth)
	}
}

func (c *comparison) mapValues(after, stored protoreflect.Message, fd protoreflect.FieldDescriptor, path string, depth int) {
	a, b := MapOf(after, fd), MapOf(stored, fd)
	keys := map[string]protoreflect.MapKey{}

	for _, m := range []protoreflect.Map{a, b} {
		if m != nil {
			m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool { keys[k.String()] = k; return true })
		}
	}

	for _, name := range slices.Sorted(maps.Keys(keys)) {
		k := keys[name]

		var av, bv protoreflect.Message
		if a != nil && a.Has(k) {
			av = a.Get(k).Message()
		}

		if b != nil && b.Has(k) {
			bv = b.Get(k).Message()
		}

		c.walk(av, bv, behavior.JoinPath(path, behavior.QuoteSegment(name)), depth)
	}
}

// Managed returns the governing update behavior, with OUTPUT_ONLY first.
func Managed(got []annotations.FieldBehavior) annotations.FieldBehavior {
	return behavior.FirstMatch(got, []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	})
}

// KeepInputSelection removes ignored selections while retaining empty keyed
// exclusions beside a wildcard. It reports whether any input selection remains.
func KeepInputSelection[M ~map[string]M](fd protoreflect.FieldDescriptor, nested M, resolve behavior.Resolve) bool {
	md := maskpath.ValueMessage(fd)
	if nested == nil || md == nil || maskpath.IsDynamicMessage(md) {
		return true
	}

	if !fd.IsList() && !fd.IsMap() {
		return keepInputFields(md, nested, resolve)
	}

	var writable bool

	for _, sub := range nested {
		if sub == nil || keepInputFields(md, sub, resolve) {
			writable = true
		}
	}

	return writable
}

func keepInputFields[M ~map[string]M](md protoreflect.MessageDescriptor, nested M, resolve behavior.Resolve) bool {
	if maskpath.IsDynamicMessage(md) {
		return len(nested) > 0
	}

	for name, sub := range nested {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd != nil && (Managed(resolve(fd)) == annotations.FieldBehavior_OUTPUT_ONLY || !KeepInputSelection(fd, sub, resolve)) {
			delete(nested, name)
		}
	}

	return len(nested) > 0
}

// DefaultAtomicMessage identifies well-known values replaced as a whole.
func DefaultAtomicMessage(md protoreflect.MessageDescriptor) bool {
	if maskpath.IsDynamicMessage(md) {
		return false
	}

	name := string(md.FullName())

	return strings.HasPrefix(name, "google.protobuf.") || strings.HasPrefix(name, "google.type.")
}

// cloneSelection preserves non-nil empty keyed exclusions beside wildcards.
func cloneSelection[M ~map[string]M](selection M) M {
	if selection == nil {
		return nil
	}

	out := make(M, len(selection))
	for key, sub := range selection {
		out[key] = cloneSelection(sub)
	}

	return out
}

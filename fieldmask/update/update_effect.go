// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// evaluateSelection checks the original selection against the complete resource,
// before any protected path is pruned. ProtectedChanges checks merge preconditions
// and compares the real merge effect, including implicit oneof clears and removed
// elements. Subsequent walks only normalize the mask and validate input contracts.
func (u *updateCheck) evaluateSelection(selection fieldmask.FieldMask, root msgPair) {
	md := root.req.Descriptor()

	u.inputSelection(selection, md)

	if u.deferred != nil && !u.verify {
		u.preparedInput = selection.Clone()
	}

	if u.refusal != nil {
		return
	}

	if !u.verify {
		u.needsCurrent = u.needsCurrent || selectionNeedsStored(selection, md)

		g := oneofGuard{policy: u.policy, topGuarded: func(fd protoreflect.FieldDescriptor) bool {
			b, skip := u.behaviorOf(fd, true)
			return skip || isImmutableBehavior(b)
		}}
		if switches := g.checkMask(selection, md, root.req, nil, false, true, ""); len(switches) > 0 {
			if u.selectionDefers(selection, md, true) {
				u.needsCurrent = true
			} else {
				for _, sw := range switches {
					u.fail(sw.violation(u.policy))
				}
			}
		}

		return
	}

	u.protectedChanges(selection, root)
}

// protectedChanges compares a merge, or the complete result when selection is nil,
// using only this check's policy and traversal budget.
func (u *updateCheck) protectedChanges(selection fieldmask.FieldMask, root msgPair) {
	top := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		b, skip := u.behaviorOf(fd, true)
		if skip {
			// Routing-only fields and secrets are not selected for merging,
			// but another member of their oneof must not clear them implicitly.
			b = annotations.FieldBehavior_IMMUTABLE
		}

		return []annotations.FieldBehavior{b}
	}
	visit := func(path string, depth int) bool {
		if u.refusal != nil {
			return false
		}

		if depth > requiredMaxDepth {
			u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", path, requiredMaxDepth, fieldbehavior.ErrMaxDepthExceeded))
			return false
		}

		u.expandVisits++

		return u.withinVisits(path)
	}

	changes, err := maskmerge.ProtectedChanges(root.req, root.cur, selection, u.policy.behaviors, "", visit, top)
	if err != nil {
		u.refuse(conversionError(err))
		return
	}

	for _, v := range changes {
		u.fail(v)
	}
}

// replacementSelection is the original full-replacement selection. Only fields
// excluded by policy are omitted; protected fields remain until evaluation.
func (u *updateCheck) replacementSelection(md protoreflect.MessageDescriptor) fieldmask.FieldMask {
	if maskpath.IsDynamicMessage(md) {
		return fieldmask.FieldMask{maskpath.Wildcard: nil}
	}

	selection := fieldmask.FieldMask{}
	fields := md.Fields()

	for i := range fields.Len() {
		fd := fields.Get(i)
		if _, skip := u.behaviorOf(fd, true); skip || u.isAddressOnly(fd, true) {
			continue
		}

		selection[string(fd.Name())] = nil
	}

	return selection
}

// selectionDefers reports whether the original selection includes a protected
// comparison. Its oneof consequences must also wait for stored state. A purely
// mutable selection retains the existing conservative stateless oneof guard.
func (u *updateCheck) selectionDefers(selection fieldmask.FieldMask, md protoreflect.MessageDescriptor, top bool) bool {
	for name, nested := range selection {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			continue
		}

		b, skip := u.behaviorOf(fd, top)
		if skip || b == annotations.FieldBehavior_OUTPUT_ONLY {
			continue
		}

		if isImmutableBehavior(b) {
			return true
		}

		child := maskpath.ValueMessage(fd)
		if child == nil || maskpath.IsDynamicMessage(child) {
			continue
		}

		if nested == nil {
			if holdsImmutable(child, map[protoreflect.FullName]bool{}, u.policy) {
				return true
			}

			continue
		}

		if fd.IsList() || fd.IsMap() {
			for _, sub := range nested {
				if (sub == nil && holdsImmutable(child, map[protoreflect.FullName]bool{}, u.policy)) || u.selectionDefers(sub, child, false) {
					return true
				}
			}
		} else if u.selectionDefers(nested, child, false) {
			return true
		}
	}

	return false
}

// selectionNeedsStored retains the state needed by merge preconditions even
// when normalization will remove every selected protected path.
func selectionNeedsStored(selection fieldmask.FieldMask, md protoreflect.MessageDescriptor) bool {
	if maskpath.IsDynamicMessage(md) {
		return len(selection) > 0
	}

	for name, nested := range selection {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil || nested == nil {
			continue
		}

		if fd.IsList() || fd.IsMap() || (fd.Message() != nil && selectionNeedsStored(nested, fd.Message())) {
			return true
		}
	}

	return false
}

// inputSelection resolves the writer's normal form and removes ignored input
// in place. The same tree is evaluated and then pruned into the writable mask.
func (u *updateCheck) inputSelection(selection fieldmask.FieldMask, md protoreflect.MessageDescriptor) {
	resolved, err := mergeSelection(md, selection.ToPaths(), u.policy)
	if err != nil {
		u.refuse(err)
		return
	}

	replaceMask(selection, resolved)

	if u.namedInput == nil {
		u.namedInput = selection.Clone()
	}

	// Dynamic roots use JSON keys, including keys that happen to match the
	// wrapper's protobuf fields. They have no declared field behavior here.
	if maskpath.IsDynamicMessage(md) {
		return
	}

	for name, nested := range selection {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			continue // only declared fields can carry a behavior
		}

		b, skip := u.behaviorOf(fd, true)
		if skip || b == annotations.FieldBehavior_OUTPUT_ONLY || !keepInputSelection(fd, nested, u.policy) {
			delete(selection, name)
		}
	}
}

// impliedSelection uses the planner's populated leaves while retaining protected
// values for validation. Empty mutable messages contribute no path.
func (u *updateCheck) impliedSelection(root msgPair) fieldmask.FieldMask {
	md := root.req.Descriptor()

	skip := append([]string(nil), u.policy.impliedSkipFields...)

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if _, ignored := u.behaviorOf(fd, true); ignored || u.isAddressOnly(fd, true) {
			skip = append(skip, string(fd.Name()))
		}
	}

	top := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		b, _ := u.behaviorOf(fd, true)
		return []annotations.FieldBehavior{b}
	}

	return fieldmask.FromPaths(maskmerge.ImpliedPaths(root.req, skip, u.policy.behaviors, u.policy.atomicMessage,
		u.policy.maxPathDepth, true, top)...)
}

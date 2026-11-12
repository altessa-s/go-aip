// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// preserveUnverified adds the request values a caller still needs to compare
// to a private filtering mask. These entries never enter the effective mask.
// Collections are kept whole so element positions and keys remain available
// for comparison; retaining them does not authorize writing them.
func preserveUnverified(keep fieldmask.FieldMask, msg protoreflect.Message, policy *options) {
	preserveUnverifiedDepth(keep, msg, policy, true, false, requiredMaxDepth)
}

// preserveSelectedUnverified keeps selected identities without widening partial
// collection selections to unselected protected fields or entries. Prepare owns
// the original input, so its handler-visible request needs no other comparisons.
func preserveSelectedUnverified(keep fieldmask.FieldMask, msg protoreflect.Message, policy *options) {
	preserveUnverifiedDepth(keep, msg, policy, true, true, requiredMaxDepth)
}

func preserveUnverifiedDepth(keep fieldmask.FieldMask, msg protoreflect.Message, policy *options, top, selectedOnly bool, room int) {
	msg.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		name := string(fd.Name())
		if _, selected := keep[name]; selectedOnly && !selected {
			return true
		}

		b := policy.serverManaged(fd)
		if top {
			b, _ = policyTopBehavior(policy)(fd)
		}

		switch {
		case b == annotations.FieldBehavior_OUTPUT_ONLY:
			return true
		case isImmutableBehavior(b):
			if selectedOnly && b == annotations.FieldBehavior_IMMUTABLE && keep[name] != nil {
				return true // a selected descendant does not select its siblings
			}

			keep[name] = nil

			return true
		}

		md := maskpath.ValueMessage(fd)
		if md == nil || !holdsImmutable(md, map[protoreflect.FullName]bool{}, policy) {
			return true
		}

		if nested, ok := keep[name]; ok && nested == nil {
			return true // already kept whole
		}

		if selectedOnly && (fd.IsList() || fd.IsMap() || room == 0) {
			// The filter recurses through element masks and keeps empty list
			// messages in place. Comparison uses Prepare's captured input.
			return true
		}

		if fd.IsList() || fd.IsMap() || isAtomicValue(fd, policy) || room == 0 {
			keep[name] = nil
			return true
		}

		nested := keep[name]
		if nested == nil {
			nested = fieldmask.FieldMask{}
		}

		preserveUnverifiedDepth(nested, value.Message(), policy, false, selectedOnly, room-1)

		if len(nested) > 0 {
			keep[name] = nested
		}

		return true
	})
}

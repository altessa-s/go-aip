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
	preserveUnverifiedDepth(keep, msg, policy, true, requiredMaxDepth)
}

func preserveUnverifiedDepth(keep fieldmask.FieldMask, msg protoreflect.Message, policy *options, top bool, room int) {
	msg.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		b := policy.serverManaged(fd)
		if top {
			b, _ = policyTopBehavior(policy)(fd)
		}

		name := string(fd.Name())

		switch {
		case b == annotations.FieldBehavior_OUTPUT_ONLY:
			return true
		case isImmutableBehavior(b):
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

		if fd.IsList() || fd.IsMap() || isAtomicValue(fd, policy) || room == 0 {
			keep[name] = nil
			return true
		}

		nested := keep[name]
		if nested == nil {
			nested = fieldmask.FieldMask{}
		}

		preserveUnverifiedDepth(nested, value.Message(), policy, false, room-1)

		if len(nested) > 0 {
			keep[name] = nested
		}

		return true
	})
}

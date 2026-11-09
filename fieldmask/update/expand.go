// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
)

// ExpandMessagePaths returns a copy of msk in which every leaf path that
// resolves (against msg) to a singular message field is expanded to cover that
// field's subtree, so [Apply] treats the masked message as a
// full replacement: sub-fields absent from msg stay in the mask and absent
// from msg, so the downstream merge clears them, instead of being left
// untouched. Without this expansion a leaf message path is kept as-provided and
// a sparse merge preserves the unspecified sub-fields of the stored resource.
//
// Expansion follows the shape of msg: a set sub-message is expanded recursively
// so its absent fields are cleared, while an absent sub-message stays a leaf:
// Apply leaves it unset (no empty message is created) and the merge
// clears it whole. Scalar, repeated, map, and dynamic well-known fields stay
// leaves; branch paths are recursed into so deeper message leaves are expanded
// too. Sub-fields carrying a REQUIRED, IMMUTABLE, IDENTIFIER, or OUTPUT_ONLY
// behavior are excluded from the expansion — a full replacement must not clear
// a protected field, and naming a REQUIRED field without a value would fail
// Apply validation. A path may use any spelling of its fields
// ([fieldmask.FieldMask.Resolve]) and comes back spelled by their declared names;
// paths that do not resolve against the schema are copied unchanged. A
// message path that stays a leaf (an absent sub-message) is expanded by
// Apply to the message's other fields when it holds an IDENTIFIER,
// IMMUTABLE or OUTPUT_ONLY field, so clearing it keeps the stored
// server-managed values.
//
// msk is not modified.
func ExpandMessagePaths(msk fieldmask.FieldMask, msg proto.Message, opts ...Option) fieldmask.FieldMask {
	if msg == nil {
		return msk.Clone()
	}

	prf := msg.ProtoReflect()

	policy := newOptions(opts...)

	return expandMessagePaths(resolveLenient(msk, prf.Descriptor(), policy.strictDynamicPaths), prf, policy)
}

// expandMessagePaths is the recursive worker behind [ExpandMessagePaths],
// matching msk against the fields of msg.
func expandMessagePaths(msk fieldmask.FieldMask, msg protoreflect.Message, policy *options) fieldmask.FieldMask {
	result := make(fieldmask.FieldMask, len(msk))
	fields := msg.Descriptor().Fields()

	for name, nested := range msk {
		fd := fields.ByName(protoreflect.Name(name))
		switch {
		case fd == nil || !policy.expandable(fd):
			// Unresolved path or non-singular-message field: keep as-is.
			result[name] = cloneSubmask(nested)
		case nested != nil:
			// Branch: follow the caller's sub-mask into the sub-message.
			result[name] = expandMessagePaths(nested, msg.Get(fd).Message(), policy)
		default:
			// Leaf message field: expand it to a full-replacement sub-mask.
			result[name] = replacementSubmask(msg, fd, map[protoreflect.FullName]bool{}, policy)
		}
	}

	return result
}

// replacementSubmask builds the sub-mask that replaces the message held in field
// fd of parent. A set sub-message is expanded recursively so its absent fields
// are cleared; an absent sub-message returns nil (stays a leaf), which
// Apply leaves unset and the merge clears whole. Fields
// carrying a REQUIRED, IMMUTABLE, IDENTIFIER, or OUTPUT_ONLY behavior are
// skipped. visited breaks recursion on self-referential schemas. Returns nil
// when nothing is expandable, so the caller falls back to leaf
// (keep-as-provided) semantics.
func replacementSubmask(parent protoreflect.Message, fd protoreflect.FieldDescriptor, visited map[protoreflect.FullName]bool,
	policy *options) fieldmask.FieldMask {
	md := fd.Message()
	if md == nil || visited[md.FullName()] || !parent.Has(fd) {
		return nil
	}

	visited[md.FullName()] = true
	defer delete(visited, md.FullName())

	sub := parent.Get(fd).Message()
	result := make(fieldmask.FieldMask)
	fields := md.Fields()

	for i := range fields.Len() {
		cfd := fields.Get(i)
		if isProtectedFromExpansion(cfd, policy) {
			continue
		}

		if policy.expandable(cfd) {
			result[string(cfd.Name())] = replacementSubmask(sub, cfd, visited, policy)
		} else {
			result[string(cfd.Name())] = nil
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

// isExpandableMessage reports whether fd is a singular message field whose
// subtree is navigable by the static schema (not a list, map, or dynamic
// well-known type).
func isExpandableMessage(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() && !maskpath.IsDynamicField(fd)
}

// isProtectedFromExpansion reports whether fd carries an update-mask behavior
// (REQUIRED, IMMUTABLE, IDENTIFIER, OUTPUT_ONLY) that a full-message replacement
// must not auto-clear.
func isProtectedFromExpansion(fd protoreflect.FieldDescriptor, policy *options) bool {
	return firstUpdateMaskBehavior(fd, policy) != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
}

// cloneSubmask deep-copies a sub-mask while preserving the nil (leaf) versus
// non-nil (branch) distinction that [fieldmask.FieldMask.Clone] would otherwise collapse.
func cloneSubmask(n fieldmask.FieldMask) fieldmask.FieldMask {
	if n == nil {
		return nil
	}

	return n.Clone()
}

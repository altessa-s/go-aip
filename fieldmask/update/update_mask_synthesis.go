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
	"github.com/altessa-s/go-aip/internal/behavior"
)

// FromSetFields returns the update mask an update request implies
// when its update_mask is omitted (AIP-134: an omitted mask updates every
// populated field), ready for [Apply], which applies it
// on its own to a nil mask. A provided empty mask is not omitted: it updates
// nothing, so pass it to Apply as is instead of synthesizing one.
// The result is [FromSetFields] of msg with the server-managed fields skipped
// (see [fieldmask.WithSkipFields]):
//   - every IDENTIFIER field, top-level and in populated singular
//     sub-messages: the identifier addresses the resource (AIP-203), it is
//     not a field the update changes;
//   - every top-level identity field of the policy ([WithIdentityFields]; id
//     and etag by default), for the same reason;
//   - every path of [WithImpliedSkipFields], top-level or nested;
//   - every OUTPUT_ONLY field of a populated sub-message: never written by
//     an update (a top-level one is dropped by Apply);
//   - the unset IMMUTABLE fields of a populated sub-message (a set one is
//     kept, to be compared, but never lets its parent be named whole: the
//     mask names the sub-message's set fields instead, so echoing an
//     unchanged IMMUTABLE value does not replace the whole sub-message);
//   - the absent singular sub-messages of a populated one whose schema holds
//     a server-managed field (OUTPUT_ONLY, IDENTIFIER or IMMUTABLE) at any
//     depth.
//
// A nested skip drops the parents that cover it, so a populated sub-message
// holding such a field is masked by its other set fields instead of whole. A
// sub-message whose only populated field is skipped drops out of the mask
// altogether. A set IMMUTABLE field, top-level or nested (a top-level field
// of [WithImmutableFields] included), is not skipped: it is part of the
// update, so Apply compares it with the stored resource of
// WithCurrent (ignored when unchanged, a violation when changed or when no
// stored resource is supplied). Repeated and map fields are named whole and
// replaced as a whole, as with [FromSetFields]. Pass the options given to
// Apply.
//
// The result can be empty: the request populates nothing to update, and
// Apply treats it as a no-op that leaves msg untouched.
func FromSetFields(msg proto.Message, opts ...Option) fieldmask.FieldMask {
	if msg == nil {
		return fieldmask.FieldMask{}
	}

	return fromSetFields(msg, newOptions(opts...))
}

// fromSetFields is [FromSetFields] under policy.
func fromSetFields(msg proto.Message, policy *options) fieldmask.FieldMask {
	if msg == nil {
		return fieldmask.FieldMask{}
	}

	m := msg.ProtoReflect()
	skip, descend := synthesisFields(m, policy)

	return fieldmask.FromPaths(maskpath.Collect(m, true, maskpath.SetFields{Skip: skip, Descend: descend}, policy.maxPathDepth)...)
}

// policySkipFields returns the skip paths the policy adds for a message of
// type md: its top-level identity fields ([isPolicyIdentity]) and the
// [WithImpliedSkipFields] paths.
func policySkipFields(md protoreflect.MessageDescriptor, policy *options) []string {
	var paths []string

	fields := md.Fields()
	for _, name := range policy.identityFields {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil && isPolicyIdentity(fd, policy) {
			paths = append(paths, name)
		}
	}

	return append(paths, policy.impliedSkipFields...)
}

// synthesisSkipFields returns the skip paths of [FromSetFields] for
// msg. Lists and maps are not descended: [FromSetFields] names them whole.
// The walk follows the populated value rather than the descriptor, so a
// recursive message type terminates at its last populated level and every
// identifier on the chain is found.
func synthesisSkipFields(msg protoreflect.Message, policy *options) []string {
	skip, _ := synthesisFields(msg, policy)
	return skip
}

// synthesisFields returns the skip paths of [FromSetFields] for msg
// ([synthesisSkipFields]) and the set nested IMMUTABLE fields, which are
// kept to be compared but must not let their parent be named whole: echoing
// an unchanged IMMUTABLE value would otherwise widen the implied mask to the
// whole sub-message, clearing its unset siblings.
func synthesisFields(msg protoreflect.Message, policy *options) (skip, descend []string) {
	skip = policySkipFields(msg.Descriptor(), policy)
	collectSkipFields(msg, "", &skip, &descend, policy)

	return skip, descend
}

// collectSkipFields appends the skip paths of msg under prefix to paths and
// its set nested IMMUTABLE fields to descend (see [synthesisFields]).
func collectSkipFields(msg protoreflect.Message, prefix string, paths, descend *[]string, policy *options) {
	fields := msg.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		path := behavior.JoinPath(prefix, string(fd.Name()))
		switch serverManagedBehavior(fd, policy) {
		case annotations.FieldBehavior_OUTPUT_ONLY:
			// Never written by the update: a nested one is skipped so its
			// parent is masked by its other set fields, a top-level one is
			// dropped by Apply. Not descended.
			if prefix != "" {
				*paths = append(*paths, path)
			}

			continue
		case annotations.FieldBehavior_IDENTIFIER:
			*paths = append(*paths, path)
			continue
		case annotations.FieldBehavior_IMMUTABLE:
			switch {
			case prefix == "":
			case !msg.Has(fd):
				*paths = append(*paths, path)
				continue
			default:
				*descend = append(*descend, path)
			}
		default:
		}

		switch {
		case !policy.expandable(fd):
		case msg.Has(fd):
			collectSkipFields(msg.Get(fd).Message(), path, paths, descend, policy)
		case prefix != "" && hasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, policy):
			// Do not select an absent protected subtree as a whole.
			*paths = append(*paths, path)
		}
	}
}

// DropIdentifiers removes the IDENTIFIER entries of msg's schema from the
// mask, descending into the sub-masks of singular message fields and into the
// element sub-masks of repeated and map fields. A branch left empty is removed
// too: it names no field.
//
// [Apply] adds the set IDENTIFIER fields to the mask so
// they survive the filter; they address the resource (AIP-203) rather than
// name fields to change, so a caller that hands the cleaned mask on — the gRPC
// fieldmask interceptor writes it back to the request — drops them first. An
// IDENTIFIER the client names in the mask is either rejected by
// Apply or, unchanged, dropped from the mask and re-added as an
// address, so every entry removed then addresses the resource.
//
// The top-level identity fields of the policy ([WithIdentityFields]; id and
// etag by default) that Apply keeps as the address are removed
// too, whether added by it or left unchanged by the client.
//
// Mutates msk. Pass the same policy used by Apply.
func DropIdentifiers(msk fieldmask.FieldMask, msg proto.Message, opts ...Option) {
	if msg == nil {
		return
	}

	dropPolicyIdentifiers(msk, msg.ProtoReflect().Descriptor(), newOptions(opts...))
}

// dropIdentifiers is [DropIdentifiers] over a descriptor. Keys md
// does not declare ("*", map keys, indexes) are left alone.
func dropIdentifiers(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, policy *options) {
	fields := md.Fields()
	for name, nested := range msk {
		fd := fields.ByName(protoreflect.Name(name))
		switch {
		case fd == nil:
			continue
		case behavior.FirstMatch(policy.behaviors(fd), []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}) == annotations.FieldBehavior_IDENTIFIER:
			delete(msk, name)
			continue
		case nested == nil:
			continue
		case policy.expandable(fd):
			dropIdentifiers(nested, fd.Message(), policy)
		case fd.IsList() || fd.IsMap():
			elem := wildcardElementDescriptor(fd, policy)
			if elem == nil {
				continue
			}

			for key, sub := range elementMasks(fd, nested) {
				dropIdentifiers(sub, elem, policy)

				if len(sub) == 0 {
					delete(nested, key)
				}
			}
		}

		if len(nested) == 0 {
			delete(msk, name)
		}
	}
}

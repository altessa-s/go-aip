// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// FromSetFields returns the populated leaf selection of msg for an update with
// an omitted mask (AIP-134). Empty mutable messages contribute no path. Lists,
// maps, atomic messages and populated IMMUTABLE fields are selected whole.
// OUTPUT_ONLY fields, routing identifiers and [WithImpliedSkipFields] are omitted.
// A provided empty update mask is a no-op; pass it to [Apply] without synthesis.
//
// Pass the same options as [Apply]. Selected IMMUTABLE values are compared with
// [WithCurrent], or retained with their paths deferred without current state.
// This helper omits identifiers from its returned mask; [Apply] still validates
// populated identifiers in visited messages. Prefer [Prepare] with a nil mask
// for a two-stage update: it retains all protected input for completion by
// [Spec.Plan] or [Patch.Run]. Never store the normalized request as the resource.
//
// The result can be empty when nothing writable is populated. [Apply] treats a
// provided empty mask as a no-op and leaves msg untouched.
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
	skip := synthesisSkipFields(m, policy)
	top := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		b, _ := policyTopBehavior(policy)(fd)
		return []annotations.FieldBehavior{b}
	}

	return fieldmask.FromPaths(maskmerge.ImpliedPaths(m, skip, policy.behaviors, policy.atomicMessage, policy.maxPathDepth, true, top)...)
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
	collectSkipFields(msg, "", maskpath.DepthRoom(policy.maxPathDepth), &skip, &descend, policy)

	return skip, descend
}

// collectSkipFields appends the skip paths of msg under prefix to paths and
// its set nested IMMUTABLE fields to descend (see [synthesisFields]). room is
// the number of segments a path under prefix may still hold: the walk stops
// where the implied paths stop ([WithMaxPathDepth]), so a path at the limit
// stands for its whole subtree, which the update then checks — or refuses —
// as a message named whole, never erased by a skip below it.
func collectSkipFields(msg protoreflect.Message, prefix string, room int, paths, descend *[]string, policy *options) {
	walk := synthesisWalk{policy, serverManagedBehavior, isProtectedAtomic, hasProtectedDescendants}
	walk.collect(msg, prefix, room, paths, descend)
}

// synthesisWalk shares the skip-path traversal while letting the base diagnostic
// walk retain its original protected-field predicates.
type synthesisWalk struct {
	policy      *options
	managed     func(protoreflect.FieldDescriptor, *options) annotations.FieldBehavior
	atomic      func(protoreflect.FieldDescriptor, *options) bool
	descendants func(protoreflect.MessageDescriptor, map[protoreflect.FullName]bool, *options) bool
}

func (w synthesisWalk) collect(msg protoreflect.Message, prefix string, room int, paths, descend *[]string) {
	if room < 1 {
		return
	}

	fields := msg.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		path := behavior.JoinPath(prefix, string(fd.Name()))

		b := w.managed(fd, w.policy)
		if prefix == "" && b == annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED && slices.Contains(w.policy.immutableFields, string(fd.Name())) {
			b = annotations.FieldBehavior_IMMUTABLE // a top-level field the policy makes immutable
		}

		switch b {
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
			default:
				*descend = append(*descend, path)
			}

			// Never descended: an IMMUTABLE value the request sets is named
			// whole and compared whole, so a sub-message neither drops out of
			// the mask nor is compared through a few of its fields.
			continue
		default:
		}

		switch {
		case prefix != "" && w.atomic(fd, w.policy):
			// A protected leaf, as a set IMMUTABLE value: named on its own and
			// compared whole when set, its parent masked by its other set
			// fields; when unset, it must not let its parent be named whole.
			if msg.Has(fd) {
				*descend = append(*descend, path)
			} else {
				*paths = append(*paths, path)
			}
		case !w.policy.expandable(fd):
		case msg.Has(fd):
			w.collect(msg.Get(fd).Message(), path, room-1, paths, descend)
		case prefix != "" && w.descendants(fd.Message(), map[protoreflect.FullName]bool{}, w.policy):
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
// [Apply] and [ApplyMask] already exclude IDENTIFIER and policy identity paths
// from their effective masks; routing values survive through a private filter
// mask. An explicitly named IDENTIFIER is accepted and compared with current
// state, or deferred without it. DropIdentifiers is only needed for caller-built
// masks and performs no comparison. Preserve a [Prepare] plan for completion
// before consuming an effective mask; removing paths alone loses clear intent.
//
// The top-level identity fields of the policy ([WithIdentityFields]; id and
// etag by default) are removed too.
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

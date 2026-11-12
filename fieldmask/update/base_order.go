// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

// This is the a8b17ba reference order. Keep the traversal semantics identical
// to the base, with concrete paths recorded at diagnostic reporting points.
// They are used only to order diagnostics, never for acceptance.
// Adapted from update_check.go, update_mask.go, update_mask_synthesis.go,
// update_path.go and internal/behavior/violation.go, with distinct private names,
// relocated internal imports and the removed unverified reason constants inlined.
// Unchanged REQUIRED and skip-path synthesis helpers remain shared, as do
// behavior.Required*, behavior.SameValue, checkPinLimits and
// wildcardElementDescriptor. Traversal order is preserved.

import (
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// Behavior sets skipped by the REQUIRED walks of an update. A replaced
// singular message keeps the stored value of an IMMUTABLE field the request
// leaves out, so only the fields a replacement writes are checked; the
// elements of a replaced collection are new values, checked as on create.
var (
	baseOrderUpdateRequiredSkip = []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	}
	baseOrderElementRequiredSkip = []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER}
)

// baseOrderRequiredMaxDepth bounds the REQUIRED walks of an update over the values it
// writes, as [fieldbehavior.WithMaxDepth] bounds them on create: a value
// nesting more messages is refused ([baseOrderUpdateCheck.required]), never checked
// only in part.
const baseOrderRequiredMaxDepth = fieldbehavior.DefaultMaxDepth

// baseOrderMsgPair is a request message and its stored counterpart at the same path.
// Either is nil when absent: the request does not hold the message, the
// stored resource does not, or no stored resource was supplied. path is the
// concrete path of the message ("", "profile", "aliases.2", `labels.k`).
type baseOrderMsgPair struct {
	req, cur protoreflect.Message
	path     string
}

// baseOrderUpdateCheck applies the AIP-203 rules of one update to its mask: it drops
// OUTPUT_ONLY entries, drops IMMUTABLE and IDENTIFIER entries whose value
// equals the stored one, expands a replaced message that holds server-managed
// fields to its other fields, and records a violation for every REQUIRED
// field the update leaves empty and every IMMUTABLE or IDENTIFIER value it
// would change or cannot verify.
type baseOrderUpdateCheck struct {
	policy *options
	// verify reports that the stored resource is known: protected values are
	// compared with it.
	verify bool
	// top, when set, overrides the behavior of top-level fields.
	top topBehavior
	// addressOnly, when set, reports the top-level fields that address the
	// resource without being compared unless the mask names them: the
	// identity fields of the policy ([WithIdentityFields]) that carry no
	// IDENTIFIER annotation.
	addressOnly func(fd protoreflect.FieldDescriptor) bool
	violations  []FieldViolation
	diagnostics []referenceDiagnostic
	// refusal is the first refusal of the mask itself (a path error, not a
	// field behavior violation) the check made, nil otherwise.
	refusal error
	// expandVisits counts the messages [baseOrderUpdateCheck.expand] visits in the
	// whole call, the validation work it bounds ([baseOrderUpdateCheck.expandWithin]);
	// expandLeaves counts the leaves of the current top-level expansion
	// (expandNesting is zero outside one), which become paths of the mask
	// unless validationOnly says the expansion only compares protected
	// values.
	expandVisits, expandLeaves, expandNesting int
	validationOnly                            bool
	// atomicPaths caches [baseOrderAtomicProtectedPath] per atomic type.
	atomicPaths map[protoreflect.FullName]baseOrderProtectedPath
}

// isAddressOnly reports whether fd, at the top level when topLevel, is one
// of u.addressOnly fields.
func (u *baseOrderUpdateCheck) isAddressOnly(fd protoreflect.FieldDescriptor, topLevel bool) bool {
	return topLevel && u.addressOnly != nil && u.addressOnly(fd)
}

// behaviorOf returns the server-managed behavior governing fd at depth
// (top-level when prefix is empty) and whether the update skips it.
func (u *baseOrderUpdateCheck) behaviorOf(fd protoreflect.FieldDescriptor, topLevel bool) (annotations.FieldBehavior, bool) {
	if topLevel && u.top != nil {
		return u.top(fd)
	}

	return u.policy.serverManaged(fd), false
}

// required returns the recorder of a REQUIRED walk of the value at path:
// it records the violations, or, when the walk exceeded
// [baseOrderRequiredMaxDepth], refuses the request with
// [fieldbehavior.ErrMaxDepthExceeded], since the REQUIRED fields below were
// not checked.
func (u *baseOrderUpdateCheck) required(path string) func(v []FieldViolation, exceeded bool) {
	return func(v []FieldViolation, exceeded bool) {
		if exceeded {
			u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", path, baseOrderRequiredMaxDepth,
				fieldbehavior.ErrMaxDepthExceeded))

			return
		}

		u.violations = append(u.violations, v...)
		for _, violation := range v {
			u.recordReference(violation)
		}
	}
}

// fail records a violation.
func (u *baseOrderUpdateCheck) fail(v FieldViolation, paths ...string) {
	u.violations = append(u.violations, v)
	u.recordReference(v, paths...)
}

// checkMask applies the update rules to msk, the sub-mask of message type md
// whose addressed messages are pairs, under the mask path prefix. It mutates
// msk: dropped entries are deleted, a branch left empty is deleted with them,
// and a replaced message holding server-managed fields becomes the sub-mask
// of its other fields.
func (u *baseOrderUpdateCheck) checkMask(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []baseOrderMsgPair, prefix string) {
	u.verifyUnnamedIdentifiers(msk, md, pairs, prefix)

	fields := md.Fields()
	for _, name := range slices.Sorted(maps.Keys(msk)) {
		fd := fields.ByName(protoreflect.Name(name))
		if fd == nil {
			continue
		}

		nested := msk[name]
		path := behavior.JoinPath(prefix, name)

		b, skip := u.behaviorOf(fd, prefix == "")
		switch {
		case skip:
			continue
		case b == annotations.FieldBehavior_OUTPUT_ONLY:
			// AIP-203: ignored in update masks at any depth, never an error.
			delete(msk, name)
			continue
		case b == annotations.FieldBehavior_IDENTIFIER || b == annotations.FieldBehavior_IMMUTABLE:
			if u.unchanged(fd, nested, pairs, path, b) {
				delete(msk, name)
			}

			continue
		}

		if nested == nil {
			u.checkWhole(msk, name, fd, pairs, path)
			continue
		}

		switch {
		case fd.IsList() || fd.IsMap():
			u.checkElements(fd, nested, pairs, path)
		case u.policy.expandable(fd):
			u.checkMask(nested, fd.Message(), baseOrderChildPairs(pairs, fd), path)
		default:
			continue
		}

		if len(nested) == 0 {
			delete(msk, name)
		}
	}
}

// unchanged reports whether the IMMUTABLE or IDENTIFIER field fd (behavior
// b), selected by the sub-mask nested, holds its stored value in every pair,
// so the update ignores it (AIP-203). A changed value, or any value while the
// stored resource is unknown, is recorded as a violation.
func (u *baseOrderUpdateCheck) unchanged(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []baseOrderMsgPair, path string, b annotations.FieldBehavior,
) bool {
	if !u.verify {
		u.fail(baseOrderProtectedViolation(path, b, false), referencePairPaths(fd, pairs)...)
		return false
	}

	for _, p := range pairs {
		equal, err := baseOrderSameSelection(fd, nested, p.req, p.cur)
		if err != nil {
			u.refuse(conversionError(err))
			return false
		}

		if !equal {
			u.fail(baseOrderProtectedViolation(path, b, true), referencePairPaths(fd, pairs)...)
			return false
		}
	}

	return true
}

// verifyUnnamedIdentifiers compares the populated IDENTIFIER fields of md
// that msk does not name with the stored ones: the update keeps them to
// address the resource, so they must not differ from it. Without a stored
// resource they are accepted as the address.
func (u *baseOrderUpdateCheck) verifyUnnamedIdentifiers(
	msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []baseOrderMsgPair, prefix string,
) {
	if !u.verify {
		return
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if _, named := msk[string(fd.Name())]; named {
			continue
		}

		if b, skip := u.behaviorOf(fd, prefix == ""); skip || b != annotations.FieldBehavior_IDENTIFIER || u.isAddressOnly(fd, prefix == "") {
			continue
		}

		for _, p := range pairs {
			if p.req != nil && p.req.Has(fd) && !behavior.SameValue(p.req, p.cur, fd) {
				u.fail(baseOrderProtectedViolation(behavior.JoinPath(prefix, string(fd.Name())), annotations.FieldBehavior_IDENTIFIER, true),
					referencePairPaths(fd, pairs)...)

				break
			}
		}
	}
}

// checkWhole applies the rules to fd selected whole at path (entry name of
// msk): a REQUIRED field must be non-empty, a replacement must satisfy the
// REQUIRED fields of the value it writes, and a singular message holding a
// server-managed field is replaced through its other fields (see
// [baseOrderUpdateCheck.expand]) so the stored server-managed values survive.
func (u *baseOrderUpdateCheck) checkWhole(msk fieldmask.FieldMask, name string, fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair, path string) {
	if slices.Contains(u.policy.behaviors(fd), annotations.FieldBehavior_REQUIRED) {
		failed := false

		for _, p := range pairs {
			if behavior.RequiredUnmet(p.req, fd) {
				u.fail(FieldViolation{Path: behavior.JoinPath(p.path, name), Behavior: annotations.FieldBehavior_REQUIRED, Reason: behavior.ReasonRequired})

				failed = true
			}
		}

		if failed {
			return
		}
	}

	u.checkWrittenRequired(fd, pairs)
	u.checkAtomicProtected(fd, pairs)

	if !u.policy.expandable(fd) || !baseOrderHasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, u.policy) {
		return
	}

	onPath := map[protoreflect.FullName]bool{fd.ContainingMessage().FullName(): true, fd.Message().FullName(): true}
	if sub := u.expand(fd.Message(), baseOrderChildPairs(pairs, fd), path, onPath); len(sub) > 0 {
		msk[name] = sub
	} else {
		delete(msk, name)
	}
}

// isAtomic reports whether md is a message type the policy keeps whole
// ([WithAtomicMessage]); the dynamic Struct, Value and ListValue never are.
func (u *baseOrderUpdateCheck) isAtomic(md protoreflect.MessageDescriptor) bool {
	return md != nil && u.policy.atomicMessage != nil && u.policy.atomicMessage(md) && !maskpath.IsDynamicMessage(md)
}

// atomicProtected is [baseOrderAtomicProtectedPath] of md, computed once per type for
// the check.
func (u *baseOrderUpdateCheck) atomicProtected(md protoreflect.MessageDescriptor) (string, annotations.FieldBehavior) {
	if p, ok := u.atomicPaths[md.FullName()]; ok {
		return p.path, p.behavior
	}

	path, b := baseOrderAtomicProtectedPath(md, map[protoreflect.FullName]bool{}, u.policy)

	if u.atomicPaths == nil {
		u.atomicPaths = map[protoreflect.FullName]baseOrderProtectedPath{}
	}

	u.atomicPaths[md.FullName()] = baseOrderProtectedPath{path: path, behavior: b}

	return path, b
}

// checkAtomicProtected checks fd, a singular message of an atomic type
// written whole in each pair, as a protected leaf: the merge copies the value
// whole, so it cannot keep a stored IMMUTABLE or IDENTIFIER field below the
// atomic boundary as an expansion keeps it in another message. When the type
// holds such a field ([baseOrderAtomicProtectedPath]), every one of them — reached
// through singular messages, a protected repeated or map field compared whole —
// must equal the stored one, absence included: the first change is a
// violation. Without the stored resource the replacement is one violation,
// since it cannot keep values it does not know (AIP-203).
func (u *baseOrderUpdateCheck) checkAtomicProtected(fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair) {
	if !baseOrderIsAtomicValue(fd, u.policy) {
		return
	}

	first, b := u.atomicProtected(fd.Message())
	if first == "" {
		return
	}

	for _, p := range pairs {
		path := behavior.JoinPath(p.path, string(fd.Name()))

		if !u.verify {
			u.fail(baseOrderProtectedViolation(behavior.JoinPath(path, first), b, false))
			continue
		}

		if diff, b, ok := u.protectedDiff(fd.Message(), behavior.Child(p.req, fd), behavior.Child(p.cur, fd), path, 0); ok {
			u.fail(baseOrderProtectedViolation(diff, b, true))
		}
	}
}

// protectedDiff returns the path and behavior of the first protected field
// ([baseOrderAtomicProtectedPath]) of the message of type md whose request and stored
// values r and c (nil when absent) differ, at path prefix. It follows the
// values, only into sub-messages of a type that holds a protected field,
// within the REQUIRED walk depth and the shared work budget; a refusal
// stops it.
func (u *baseOrderUpdateCheck) protectedDiff(md protoreflect.MessageDescriptor, r, c protoreflect.Message, prefix string, depth int,
) (string, annotations.FieldBehavior, bool) {
	if depth > baseOrderRequiredMaxDepth {
		u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", prefix, baseOrderRequiredMaxDepth,
			fieldbehavior.ErrMaxDepthExceeded))

		return "", 0, false
	}

	if u.expandVisits++; !u.withinVisits(prefix) {
		return "", 0, false
	}

	fields := md.Fields()
	for i := range fields.Len() {
		f := fields.Get(i)
		path := behavior.JoinPath(prefix, string(f.Name()))

		switch b := u.policy.serverManaged(f); b { //nolint:exhaustive // serverManaged returns only these or UNSPECIFIED.
		case annotations.FieldBehavior_OUTPUT_ONLY:
			continue
		case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
			if !behavior.SameValue(r, c, f) {
				return path, b, true
			}

			continue
		}

		if f.IsList() || f.IsMap() || f.Message() == nil || maskpath.IsDynamicMessage(f.Message()) {
			continue
		}

		if sub, _ := u.atomicProtected(f.Message()); sub == "" {
			continue // nothing below to keep
		}

		rc, cc := behavior.Child(r, f), behavior.Child(c, f)
		if rc == nil && cc == nil {
			continue
		}

		if diff, b, ok := u.protectedDiff(f.Message(), rc, cc, path, depth+1); ok || u.refusal != nil {
			return diff, b, ok
		}
	}

	return "", 0, false
}

// checkWrittenRequired records the REQUIRED fields left empty in the value of
// fd each pair's request writes: the populated subtree of a singular message
// (its IMMUTABLE fields excepted, which keep their stored value when absent)
// and every element of a repeated or map field of messages.
func (u *baseOrderUpdateCheck) checkWrittenRequired(fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair) {
	for _, p := range pairs {
		if p.req == nil || !p.req.Has(fd) {
			continue
		}

		path := behavior.JoinPath(p.path, string(fd.Name()))
		checkWrittenRequired(p.req, fd, path, u.policy.behaviors, u.isAtomic, u.required)
	}
}

// expand returns the sub-mask that replaces a singular message of type md
// (whose request and stored values are pairs, at the mask path prefix) while
// keeping its server-managed values: every field but the OUTPUT_ONLY ones,
// and a sub-message holding a server-managed field replaced the same way.
// An IMMUTABLE or IDENTIFIER field the request leaves unset keeps its stored
// value; one the request sets is compared with it — an equal value is
// ignored, a different one, or an IMMUTABLE one while the stored resource is
// unknown, is a violation; an IDENTIFIER set while it is unknown is kept as
// the address.
//
// A sub-message neither the request nor the stored resource holds is left
// out: there is nothing to replace. A member of a real oneof is written only
// when a request sets it or, to clear it, the stored resource holds it, so a
// replacement switches no oneof by itself. Without a stored resource a sub-message
// the request does not hold is expanded by its schema, except a message type
// already on the path (onPath), which has no finite expansion and is left as
// stored.
func (u *baseOrderUpdateCheck) expand(
	md protoreflect.MessageDescriptor, pairs []baseOrderMsgPair, prefix string, onPath map[protoreflect.FullName]bool,
) fieldmask.FieldMask {
	if !u.expandWithin(prefix) {
		return nil
	}

	if u.expandNesting == 0 {
		u.expandLeaves = 0 // each top-level expansion is a mask of its own
	}

	u.expandNesting++
	defer func() { u.expandNesting-- }()

	out := fieldmask.FieldMask{}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := string(fd.Name())
		path := behavior.JoinPath(prefix, name)

		b, skip := u.behaviorOf(fd, prefix == "")
		switch {
		case skip, b == annotations.FieldBehavior_OUTPUT_ONLY, u.isAddressOnly(fd, prefix == ""):
			continue
		case b == annotations.FieldBehavior_IDENTIFIER || b == annotations.FieldBehavior_IMMUTABLE:
			u.checkSetProtected(fd, pairs, path, b)
			continue
		case !u.writesOneofMember(fd, pairs):
			continue
		}

		if !u.policy.expandable(fd) || !baseOrderHasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, u.policy) {
			// A comparison-only expansion writes nothing: an atomic value
			// is compared only when the request supplies it.
			if reqHas, _ := baseOrderPairsHold(pairs, fd); !u.validationOnly || reqHas {
				u.checkAtomicProtected(fd, pairs)
			}

			if !u.emitLeaf(path) {
				return nil
			}

			out[name] = nil

			continue
		}

		reqHas, curHas := baseOrderPairsHold(pairs, fd)

		child := fd.Message().FullName()
		switch {
		case u.validationOnly && !reqHas:
			continue // only the protected values the request sets are compared
		case (!reqHas && !curHas && u.verify) || (!reqHas && !curHas && onPath[child]):
			continue
		}

		// child may already be on the path (an ancestor of the same type the
		// request or the stored resource holds): only an entry added here is
		// removed, so the ancestor stays guarded for the siblings that follow.
		wasOnPath := onPath[child]
		onPath[child] = true
		sub := u.expand(fd.Message(), baseOrderChildPairs(pairs, fd), path, onPath)

		if !wasOnPath {
			delete(onPath, child)
		}

		if len(sub) > 0 {
			out[name] = sub
		}
	}

	return out
}

// expandWithin counts a visit of [baseOrderUpdateCheck.expand] to the message at
// prefix and reports whether the traversal stays within its own bounds,
// recording the refusal otherwise: the messages nested no deeper than the
// REQUIRED walks check ([baseOrderRequiredMaxDepth], refused with
// [fieldbehavior.ErrMaxDepthExceeded]) and the messages visited in the whole
// call within the work budget ([baseOrderUpdateCheck.withinVisits]). These bound the
// traversal apart from the limits of the effective mask, which apply to the
// leaves it emits ([baseOrderUpdateCheck.emitLeaf]) and to the mask as a whole
// afterwards. An acyclic schema that branches at every level would otherwise
// expand exponentially before the effective mask could be checked; the
// expansion stops at the first excess instead.
func (u *baseOrderUpdateCheck) expandWithin(prefix string) bool {
	if u.refusal != nil {
		return false
	}

	u.expandVisits++

	// The traversal is bounded on its own: a message nesting deeper than the
	// REQUIRED walks check is refused, and so is work past the visit budget.
	// The path depth limit applies to the paths emitted ([baseOrderUpdateCheck.emitLeaf]),
	// so a message at the limit whose fields are all protected expands to
	// nothing, as it emits no path.
	if u.expandNesting > baseOrderRequiredMaxDepth {
		u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", prefix, baseOrderRequiredMaxDepth,
			fieldbehavior.ErrMaxDepthExceeded))

		return false
	}

	return u.withinVisits(prefix)
}

// emitLeaf counts the leaf at path [baseOrderUpdateCheck.expand] emits and reports
// whether it fits the limits of the effective mask, recording the refusal
// otherwise: no deeper than [WithMaxPathDepth], and at most [WithMaxPaths]
// leaves per top-level expansion. A validation-only expansion emits no path
// and is held to neither.
func (u *baseOrderUpdateCheck) emitLeaf(path string) bool {
	if u.validationOnly {
		return true
	}

	if depth := u.policy.maxPathDepth; depth > 0 && maskpath.Depth(path) > depth {
		u.refuse(&fieldmask.ValidationError{Path: path, Cause: fieldmask.ErrMaxPathDepthExceeded, Reason: fmt.Sprintf(
			"the replacement writes %q, deeper than the limit of %d segments", path, depth)})

		return false
	}

	if u.expandLeaves++; u.policy.maxPaths > 0 && u.expandLeaves > u.policy.maxPaths {
		u.refuse(&fieldmask.ValidationError{Path: path, Cause: fieldmask.ErrMaxPathsExceeded, Reason: fmt.Sprintf(
			"the replacement expands to more than %d paths, the limit", u.policy.maxPaths)})

		return false
	}

	return true
}

// withinVisits reports whether the messages [baseOrderUpdateCheck.expand] visited in
// the whole call stay within the validation work budget, recording the
// refusal otherwise. The work of every expansion — those that only compare
// protected values and those of covered paths included — is bounded apart
// from the effective mask, which is checked on its own: the larger of
// [WithMaxPaths] and [fieldmask.DefaultMaxPaths] times the path depth limit.
func (u *baseOrderUpdateCheck) withinVisits(prefix string) bool {
	depth := u.policy.maxPathDepth
	if depth <= 0 {
		depth = fieldmask.DefaultMaxPathDepth
	}

	if paths := u.policy.maxPaths; paths > 0 && u.expandVisits > max(paths, fieldmask.DefaultMaxPaths)*depth {
		budget := max(paths, fieldmask.DefaultMaxPaths) * depth
		u.refuse(&fieldmask.ValidationError{Path: prefix, Cause: fieldmask.ErrMaxPathsExceeded, Reason: fmt.Sprintf(
			"the replacement visits more than %d messages, the work %d paths of at most %d segments bound", budget,
			max(paths, fieldmask.DefaultMaxPaths), depth)})

		return false
	}

	return true
}

// writesOneofMember reports whether a replacement writes fd: any field but a
// member of a real oneof, which it writes only when a request sets it, or —
// to clear it — when the stored resource holds it. A replacement thus never
// switches a oneof the request leaves unset.
func (u *baseOrderUpdateCheck) writesOneofMember(fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair) bool {
	if od := fd.ContainingOneof(); od == nil || od.IsSynthetic() {
		return true
	}

	reqHas, curHas := baseOrderPairsHold(pairs, fd)

	return reqHas || (u.verify && curHas)
}

// checkSetProtected compares the IMMUTABLE or IDENTIFIER field fd (behavior
// b) that a replacement or full update covers without naming it: unset in
// every request it keeps the stored value; set, it must equal the stored
// value. While the stored resource is unknown a set IMMUTABLE value is a
// violation and a set IDENTIFIER is accepted as the address.
func (u *baseOrderUpdateCheck) checkSetProtected(fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair, path string, b annotations.FieldBehavior) {
	if reqHas, _ := baseOrderPairsHold(pairs, fd); !reqHas {
		return
	}

	if !u.verify {
		if b == annotations.FieldBehavior_IMMUTABLE {
			u.fail(baseOrderProtectedViolation(path, b, false), referencePairPaths(fd, pairs)...)
		}

		return
	}

	for _, p := range pairs {
		if p.req != nil && p.req.Has(fd) && !behavior.SameValue(p.req, p.cur, fd) {
			u.fail(baseOrderProtectedViolation(path, b, true), referencePairPaths(fd, pairs)...)
			return
		}
	}
}

// replaceAll returns the mask of a full replacement ("*") of the root pair's
// message type md: [baseOrderUpdateCheck.expand] of the root, after the REQUIRED
// fields of the request are checked as a replacement writes them. The
// replacement does not write a top-level field the update skips or governs
// as OUTPUT_ONLY, IDENTIFIER or IMMUTABLE — by annotation or by the policy
// ([WithImmutableFields], [WithIdentityFields]) — nor anything below it: an
// unset one keeps its stored value and a set one is compared, so its REQUIRED
// fields, and those below it, are not checked.
func (u *baseOrderUpdateCheck) replaceAll(md protoreflect.MessageDescriptor, root baseOrderMsgPair) fieldmask.FieldMask {
	// A top-level field the replacement does not write is skipped before the
	// walk baseOrderDescends into it: its value, however deep, is compared by expand
	// below, never checked for REQUIRED fields it does not write.
	v, exceeded := behavior.ReplacedRequiredViolations(root.req, "", u.policy.behaviors,
		baseOrderUpdateRequiredSkip, baseOrderElementRequiredSkip, baseOrderRequiredMaxDepth,
		u.notWritten, u.isAtomic)
	if exceeded {
		u.required("")(nil, true)
		return nil
	}

	for _, violation := range v {
		u.fail(violation)
	}

	return u.expand(md, []baseOrderMsgPair{root}, "", map[protoreflect.FullName]bool{md.FullName(): true})
}

// notWritten reports whether a replacement leaves the top-level field fd to
// its stored value: one the update skips, or one it governs as OUTPUT_ONLY,
// IDENTIFIER or IMMUTABLE (see [baseOrderUpdateCheck.expand]).
func (u *baseOrderUpdateCheck) notWritten(fd protoreflect.FieldDescriptor) bool {
	if fd == nil {
		return false
	}

	b, skip := u.behaviorOf(fd, true)

	return skip || b == annotations.FieldBehavior_OUTPUT_ONLY || isImmutableBehavior(b) || u.isAddressOnly(fd, true)
}

// checkElements applies the rules to nested, the sub-mask of the repeated or
// map field fd, for the elements it addresses (see [baseOrderElementMasks]): a
// sub-mask through a key or the "*" wildcard is checked against the element
// pairs it addresses, and an element named whole is a replacement whose
// REQUIRED fields are checked. A sub-mask emptied by the check is deleted
// once every element is checked: the elements "*" addresses are those no key
// named in the mask as sent, whichever key a check empties and whatever its
// spelling sorts before "*" ("", punctuation).
func (u *baseOrderUpdateCheck) checkElements(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []baseOrderMsgPair, path string) {
	if maskpath.ValueMessage(fd) == nil {
		return // scalar elements: nothing to check
	}

	// named is the selection as sent; nested is cleaned below.
	named := maps.Clone(nested)

	// An element named whole is a new value, checked as on create whatever
	// its type: an atomic or dynamic one too, which no sub-path may enter.
	for _, key := range slices.Sorted(maps.Keys(named)) {
		if named[key] == nil && (fd.IsMap() || key == maskpath.Wildcard) {
			u.checkWholeElements(fd, key, named, pairs)
		}
	}

	elem := wildcardElementDescriptor(fd, u.policy)
	if elem == nil {
		return
	}

	var emptied []string

	for key, sub := range baseOrderElementMasks(fd, named) {
		u.checkMask(sub, elem, baseOrderElementPairs(fd, key, named, pairs), baseOrderElementPath(path, key))

		if len(sub) == 0 {
			emptied = append(emptied, key)
		}
	}

	for _, key := range emptied {
		delete(nested, key)
	}

	if fd.IsMap() {
		u.pinWildcard(fd, nested, slices.Collect(maps.Keys(named)), pairs, path)
	}
}

// pinWildcard keeps the "*" sub-mask of nested, the sub-mask of the map
// field fd, off the entries other keys named (named, the keys before the
// check) when the check emptied the sub-mask of such a key: that entry was
// excluded from the "*" when it was checked, and without its key the "*"
// would select it on write. The "*" is then replaced by the keys of the
// entries it addresses, the request entries no other key names, each with a
// copy of its sub-mask, as [MergePaths] and the filter read it. A "*" that
// addresses no request entry is dropped. The pairs share one mask, so a map
// below several elements whose requests hold such entries cannot be pinned
// this way: the mask is refused as a *fieldmask.ValidationError (matching
// [fieldmask.ErrInvalidPath]), a path error, not a field behavior one.
func (u *baseOrderUpdateCheck) pinWildcard(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, named []string, pairs []baseOrderMsgPair, path string,
) {
	wildcard := nested[maskpath.Wildcard]
	if wildcard == nil || !slices.ContainsFunc(named, func(key string) bool {
		_, kept := nested[key]
		return key != maskpath.Wildcard && !kept
	}) {
		return
	}

	// The "*" addresses the request entries no key names, in each pair.
	var keys []string

	holders := 0

	for _, p := range pairs {
		if p.req == nil || !p.req.Has(fd) {
			continue
		}

		var pk []string

		for _, k := range behavior.SortedMapKeys(p.req.Get(fd).Map()) {
			key := k.String()
			if key == maskpath.Wildcard { // a literal "*" key, which no key path can name
				u.refuse(&fieldmask.ValidationError{Path: baseOrderElementPath(path, maskpath.Wildcard), Reason: "a key path beside \"*\" that " +
					"updates nothing cannot be kept out of the wildcard when the request holds an entry with the literal key \"*\", which no " +
					"path can name; name the entries to update"})

				return
			}

			if !slices.Contains(named, key) {
				pk = append(pk, key)
			}
		}

		if len(pk) > 0 {
			holders++
			keys = pk
		}
	}

	switch {
	case holders == 0:
		delete(nested, maskpath.Wildcard) // it addresses nothing: dropping it is exact
		return
	case len(pairs) > 1:
		u.refuse(&fieldmask.ValidationError{Path: baseOrderElementPath(path, maskpath.Wildcard), Reason: "a key path beside \"*\" that updates " +
			"nothing cannot be kept out of the wildcard below the elements of another collection; name the entries to update"})

		return
	}

	delete(nested, maskpath.Wildcard)

	if err := u.policy.checkPinLimits(path, keys, wildcard); err != nil {
		u.refuse(err)
		return
	}

	for _, key := range keys {
		nested[key] = wildcard.Clone()
	}
}

// refuse records err, a refusal of the mask itself, unless one is recorded.
func (u *baseOrderUpdateCheck) refuse(err error) {
	if u.refusal == nil || errors.Is(err, ErrMessageMismatch) {
		u.refusal = err
	}
}

// checkWholeElements records the REQUIRED fields left empty in the elements
// of fd that the sub-mask key of nested names whole: every element for "*"
// (the map entries no other key names), the entry of a map key.
func (u *baseOrderUpdateCheck) checkWholeElements(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []baseOrderMsgPair) {
	for _, p := range baseOrderElementPairs(fd, key, nested, pairs) {
		if p.req != nil {
			u.required(p.path)(behavior.RequiredViolations(p.req, p.path, u.policy.behaviors, baseOrderElementRequiredSkip, baseOrderRequiredMaxDepth))
		}
	}
}

// baseOrderElementPairs returns the element pairs of the repeated or map field fd that
// the element sub-mask under key of nested addresses in each pair's request:
// for a list every element ("*"), paired with the stored element of the
// same index; for a map with "*" every entry whose key no other sub-mask
// names, and for any other key its entry, paired with the stored entry of the
// same key. A map key the request does not hold still addresses the stored
// entry, with no request element, and a request without the list addresses
// every stored element, with none: the merge clears the fields the sub-path
// names in each.
func baseOrderElementPairs(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []baseOrderMsgPair) []baseOrderMsgPair {
	var out []baseOrderMsgPair

	for _, p := range pairs {
		path := behavior.JoinPath(p.path, string(fd.Name()))

		if fd.IsList() {
			var stored protoreflect.List
			if p.cur != nil && p.cur.Has(fd) {
				stored = p.cur.Get(fd).List()
			}

			if p.req == nil || !p.req.Has(fd) {
				// An empty request list updates every stored element in place
				// from nothing (MergePaths allows an empty source): the
				// fields the sub-path names are cleared in each of them, so
				// each is checked with no request element.
				for i := range baseOrderListLen(stored) {
					out = append(out, baseOrderMsgPair{cur: stored.Get(i).Message(), path: behavior.JoinPath(path, strconv.Itoa(i))})
				}

				continue
			}

			list := p.req.Get(fd).List()
			for i := range list.Len() {
				e := baseOrderMsgPair{req: list.Get(i).Message(), path: behavior.JoinPath(path, strconv.Itoa(i))}
				if stored != nil && i < stored.Len() {
					e.cur = stored.Get(i).Message()
				}

				out = append(out, e)
			}

			continue
		}

		out = append(out, baseOrderMapElementPairs(fd, key, nested, p, path)...)
	}

	return out
}

// baseOrderMapElementPairs is [baseOrderElementPairs] for one pair p of a map field.
func baseOrderMapElementPairs(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, p baseOrderMsgPair, path string) []baseOrderMsgPair {
	var reqMap, curMap protoreflect.Map
	if p.req != nil && p.req.Has(fd) {
		reqMap = p.req.Get(fd).Map()
	}

	if p.cur != nil && p.cur.Has(fd) {
		curMap = p.cur.Get(fd).Map()
	}

	entry := func(k protoreflect.MapKey) baseOrderMsgPair {
		e := baseOrderMsgPair{path: behavior.JoinPath(path, behavior.QuoteSegment(k.String()))}
		if reqMap != nil {
			if v := reqMap.Get(k); v.IsValid() {
				e.req = v.Message()
			}
		}

		if curMap != nil {
			if v := curMap.Get(k); v.IsValid() {
				e.cur = v.Message()
			}
		}

		return e
	}

	if key != maskpath.Wildcard {
		k, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), key)
		if !ok {
			return nil
		}

		return []baseOrderMsgPair{entry(k)}
	}

	if reqMap == nil {
		return nil
	}

	var out []baseOrderMsgPair

	for _, k := range behavior.SortedMapKeys(reqMap) {
		if _, named := baseOrderKeyMask(nested, k.String()); !named {
			out = append(out, entry(k))
		}
	}

	return out
}

// baseOrderChildPairs returns the pairs of the message field fd of each pair.
func baseOrderChildPairs(pairs []baseOrderMsgPair, fd protoreflect.FieldDescriptor) []baseOrderMsgPair {
	out := make([]baseOrderMsgPair, len(pairs))
	for i, p := range pairs {
		out[i] = baseOrderMsgPair{req: behavior.Child(p.req, fd), cur: behavior.Child(p.cur, fd), path: behavior.JoinPath(p.path, string(fd.Name()))}
	}

	return out
}

// baseOrderPairsHold reports whether any request, and any stored message, of pairs
// populates fd.
func baseOrderPairsHold(pairs []baseOrderMsgPair, fd protoreflect.FieldDescriptor) (reqHas, curHas bool) {
	for _, p := range pairs {
		reqHas = reqHas || (p.req != nil && p.req.Has(fd))
		curHas = curHas || (p.cur != nil && p.cur.Has(fd))
	}

	return reqHas, curHas
}

// baseOrderSameSelection reports whether the part of field fd that the sub-mask nested
// selects (the whole field when nested is nil) is equal in r and c, either of
// which may be nil (holding nothing).
func baseOrderSameSelection(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, r, c protoreflect.Message) (bool, error) {
	if nested == nil {
		return behavior.SameValue(r, c, fd), nil
	}

	tmpl := r
	if tmpl == nil {
		tmpl = c
	}

	if tmpl == nil {
		return true, nil
	}

	selection := fieldmask.FieldMask{string(fd.Name()): nested}

	request, err := baseOrderSelectField(tmpl, fd, r, selection)
	if err != nil {
		return false, err
	}

	stored, err := baseOrderSelectField(tmpl, fd, c, selection)
	if err != nil {
		return false, err
	}

	return proto.Equal(request, stored), nil
}

// baseOrderSelectField returns a new message of tmpl's type holding a copy of field fd
// of m (nothing when m is nil), filtered by selection. Only the field is
// copied, never the rest of m.
func baseOrderSelectField(
	tmpl protoreflect.Message, fd protoreflect.FieldDescriptor, m protoreflect.Message, selection fieldmask.FieldMask,
) (proto.Message, error) {
	holder := tmpl.Type().New()
	if m != nil && m.Has(fd) {
		if err := maskmerge.SetValue(holder, fd, m.Get(fd)); err != nil {
			return nil, err
		}
	}

	msg := holder.Interface()
	baseOrderFilter(selection, msg, false)

	return msg, nil
}

type baseOrderProtectedPath struct {
	path     string
	behavior annotations.FieldBehavior
}

// checkCovering applies the update rules to work, the mask of an update's
// paths of message type md, and then to each of covered, the single-path
// masks of the paths work covers ([coverPaths]): a covered path is checked
// as if sent alone ("profile.id" under "profile" names an IDENTIFIER), but
// only work is applied. A violation both report is recorded once.
func (u *baseOrderUpdateCheck) checkCovering(
	work fieldmask.FieldMask, covered []fieldmask.FieldMask, md protoreflect.MessageDescriptor, root baseOrderMsgPair,
) {
	u.checkMask(work, md, []baseOrderMsgPair{root}, "")

	if len(covered) == 0 {
		return
	}

	for _, m := range covered {
		u.checkMask(m, md, []baseOrderMsgPair{root}, "")
	}

	seen := make(map[FieldViolation]bool, len(u.violations))
	u.violations = slices.DeleteFunc(u.violations, func(v FieldViolation) bool {
		dup := seen[v]
		seen[v] = true

		return dup
	})
}

// baseOrderHasProtectedDescendants reports whether [protectedDescendants] would report
// a violation for md: whether md holds a server-managed field directly or in a
// singular sub-message at any depth. visited breaks recursion on
// self-referential schemas.
func baseOrderHasProtectedDescendants(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options) bool {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if baseOrderServerManagedBehavior(fd, policy) != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return true
		}

		if policy.expandable(fd) && !visited[fd.Message().FullName()] && baseOrderHasProtectedDescendants(fd.Message(), visited, policy) {
			return true
		}

		// An atomic value holding a protected field is compared, not
		// expanded: the message holding it is expanded to reach it.
		if baseOrderIsProtectedAtomic(fd, policy) {
			return true
		}
	}

	return false
}

// baseOrderIsAtomicValue reports whether fd is a singular message field of a type the
// policy keeps whole ([WithAtomicMessage]); the dynamic Struct, Value and
// ListValue never are.
func baseOrderIsAtomicValue(fd protoreflect.FieldDescriptor, policy *options) bool {
	md := fd.Message()

	return !fd.IsList() && !fd.IsMap() && md != nil && policy.atomicMessage != nil && policy.atomicMessage(md) && !maskpath.IsDynamicMessage(md)
}

// baseOrderIsProtectedAtomic reports whether fd holds a protected leaf: a singular
// value of an atomic type that holds a field a whole replacement must keep
// ([baseOrderAtomicProtectedPath]). Such a value is one protected leaf everywhere — it
// is compared whole, guards its oneof, keeps its parent expanded and is named
// on its own in an implied mask.
func baseOrderIsProtectedAtomic(fd protoreflect.FieldDescriptor, policy *options) bool {
	return baseOrderIsAtomicValue(fd, policy) && baseOrderIsProtectedAtomicType(fd.Message(), policy)
}

// baseOrderIsProtectedAtomicType reports whether md is an atomic type holding a field a
// whole replacement of it must keep ([baseOrderAtomicProtectedPath]).
func baseOrderIsProtectedAtomicType(md protoreflect.MessageDescriptor, policy *options) bool {
	if md == nil || policy.atomicMessage == nil || !policy.atomicMessage(md) || maskpath.IsDynamicMessage(md) {
		return false
	}

	path, _ := baseOrderAtomicProtectedPath(md, map[protoreflect.FullName]bool{}, policy)

	return path != ""
}

// baseOrderAtomicProtectedPath returns the path below a value of md, written whole,
// of the first field that value must keep, with its behavior, or "" when there
// is none: an IMMUTABLE or IDENTIFIER field of md or of a singular
// sub-message at any depth, a repeated or map field included (compared
// whole). OUTPUT_ONLY fields and the elements of other collections, which a
// replacement writes as new values, are not descended. visited breaks
// recursion on self-referential schemas.
func baseOrderAtomicProtectedPath(
	md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options,
) (string, annotations.FieldBehavior) {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		switch b := baseOrderServerManagedBehavior(fd, policy); b { //nolint:exhaustive // baseOrderServerManagedBehavior returns only these or UNSPECIFIED.
		case annotations.FieldBehavior_OUTPUT_ONLY:
			continue
		case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
			return string(fd.Name()), b
		}

		if fd.IsList() || fd.IsMap() || fd.Message() == nil || maskpath.IsDynamicMessage(fd.Message()) || visited[fd.Message().FullName()] {
			continue
		}

		if sub, b := baseOrderAtomicProtectedPath(fd.Message(), visited, policy); sub != "" {
			return behavior.JoinPath(string(fd.Name()), sub), b
		}
	}

	return "", annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
}

// baseOrderServerManagedBehavior returns the server-managed behavior of fd that
// governs an update: OUTPUT_ONLY, then IDENTIFIER, then IMMUTABLE, whatever
// the declaration order, so OUTPUT_ONLY names a field that is also IDENTIFIER
// or IMMUTABLE. It returns FIELD_BEHAVIOR_UNSPECIFIED for any other field.
func baseOrderServerManagedBehavior(fd protoreflect.FieldDescriptor, policy *options) annotations.FieldBehavior {
	return behavior.FirstMatch(policy.behaviors(fd), []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	})
}

// baseOrderElementPath returns the path of the element sub-mask that [baseOrderElementMasks]
// yields under key.
func baseOrderElementPath(prefix, key string) string {
	return behavior.JoinPath(prefix, behavior.QuoteSegment(key))
}

// baseOrderElementMasks yields the parts of nested, the sub-mask of the repeated or
// map field fd, that address fields of its element messages, each with the
// mask key leading to it. For a list: the "*" sub-mask, the only way a path
// addresses the elements of a repeated field (AIP-161: no index and no
// implicit per-element form). For a map: the sub-mask of every key, "*"
// included, in sorted order. A nil sub-mask names whole elements, which are
// replaced as a whole like the collection itself, and is not yielded.
func baseOrderElementMasks(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask) iter.Seq2[string, fieldmask.FieldMask] {
	return func(yield func(string, fieldmask.FieldMask) bool) {
		if fd.IsList() {
			if sub := nested[maskpath.Wildcard]; sub != nil {
				yield(maskpath.Wildcard, sub)
			}

			return
		}

		for _, key := range slices.Sorted(maps.Keys(nested)) {
			if sub := nested[key]; sub != nil && !yield(key, sub) {
				return
			}
		}
	}
}

// baseOrderPolicySkipFields returns the skip paths the policy adds for a message of
// type md: its top-level identity fields ([isPolicyIdentity]) and the
// [WithImpliedSkipFields] paths.
func baseOrderPolicySkipFields(md protoreflect.MessageDescriptor, policy *options) []string {
	var paths []string

	fields := md.Fields()
	for _, name := range policy.identityFields {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil && isPolicyIdentity(fd, policy) {
			paths = append(paths, name)
		}
	}

	return append(paths, policy.impliedSkipFields...)
}

// baseOrderSynthesisFields returns the skip paths of [FromSetFields] for msg
// ([synthesisSkipFields]) and the set nested IMMUTABLE fields, which are
// kept to be compared but must not let their parent be named whole: echoing
// an unchanged IMMUTABLE value would otherwise widen the implied mask to the
// whole sub-message, clearing its unset siblings.
func baseOrderSynthesisFields(msg protoreflect.Message, policy *options) (skip, descend []string) {
	skip = baseOrderPolicySkipFields(msg.Descriptor(), policy)
	baseOrderCollectSkipFields(msg, "", maskpath.DepthRoom(policy.maxPathDepth), &skip, &descend, policy)

	return skip, descend
}

// baseOrderCollectSkipFields appends the skip paths of msg under prefix to paths and
// its set nested IMMUTABLE fields to descend (see [baseOrderSynthesisFields]). room is
// the number of segments a path under prefix may still hold: the walk stops
// where the implied paths stop ([WithMaxPathDepth]), so a path at the limit
// stands for its whole subtree, which the update then checks — or refuses —
// as a message named whole, never erased by a skip below it.
func baseOrderCollectSkipFields(msg protoreflect.Message, prefix string, room int, paths, descend *[]string, policy *options) {
	walk := synthesisWalk{policy, baseOrderServerManagedBehavior, baseOrderIsProtectedAtomic, baseOrderHasProtectedDescendants}
	walk.collect(msg, prefix, room, paths, descend)
}

func baseOrderImpliedPaths(resource proto.Message, skip []string, policy *options) []string {
	if resource == nil {
		return nil
	}

	md := resource.ProtoReflect().Descriptor()

	var out []string

	setFields := baseOrderCollect(resource.ProtoReflect(), true, baseOrderSetFields{DeclarationOrder: true, Skip: skip}, policy.maxPathDepth)
	for _, p := range setFields {
		if leaf := baseOrderLeafOf(md, p, policy); leaf != "" && !slices.Contains(out, leaf) && !baseOrderCoversSkipped(leaf, skip) {
			out = append(out, leaf)
		}
	}

	return out
}

// baseOrderCoversSkipped reports whether the leaf path covers a skipped path: the
// leaf itself, or a skip beneath it — a field inside a well-known type or a
// collection, which baseOrderLeafOf collapses into the leaf that replaces it whole,
// so the leaf would bring the skipped field back.
func baseOrderCoversSkipped(leaf string, skip []string) bool {
	return slices.ContainsFunc(skip, func(s string) bool {
		return s == leaf || strings.HasPrefix(s, leaf+maskpath.Separator)
	})
}

// baseOrderLeafOf returns the leaf path a populated-field path of md belongs to: the
// path cut at its first repeated, map, scalar, enum or well-known-type field.
// It returns "" for a path that ends at a plain message (its leaves are
// listed on their own), runs through a server-managed field or names no
// field. A path the depth limit cut ([WithMaxPathDepth]) is a leaf even at a
// plain message: it stands for the subtree below it.
func baseOrderLeafOf(md protoreflect.MessageDescriptor, path string, policy *options) string {
	segs := strings.Split(path, maskpath.Separator)
	for i, seg := range segs {
		fd := md.Fields().ByName(protoreflect.Name(seg))
		if fd == nil || policy.serverManaged(fd) != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return ""
		}

		if !baseOrderDescends(fd, policy) {
			return strings.Join(segs[:i+1], maskpath.Separator)
		}

		md = fd.Message()
	}

	if policy.maxPathDepth > 0 && len(segs) >= policy.maxPathDepth {
		return path
	}

	return ""
}

// baseOrderDescends reports whether fd is a singular message field whose subfields
// are update paths of their own: not repeated, not a map and not a
// well-known type (google.protobuf.*, google.type.*), which is a leaf.
func baseOrderDescends(fd protoreflect.FieldDescriptor, policy *options) bool {
	if fd.Message() == nil || fd.IsList() || fd.IsMap() {
		return false
	}

	return policy.atomicMessage == nil || !policy.atomicMessage(fd.Message())
}

// baseOrderProtectedViolation returns the violation of the IMMUTABLE or IDENTIFIER
// field at path (behavior b) an update cannot accept: changed when the stored
// value is known (verified), unverifiable otherwise.
func baseOrderProtectedViolation(path string, b annotations.FieldBehavior, verified bool) FieldViolation {
	reason := "immutable field cannot be modified"

	switch {
	case b == annotations.FieldBehavior_IDENTIFIER && verified:
		reason = behavior.ReasonIdentifierChanged
	case b == annotations.FieldBehavior_IDENTIFIER:
		reason = "identifier field cannot be modified"
	case verified:
		reason = behavior.ReasonImmutableChanged
	}

	return FieldViolation{Path: path, Behavior: b, Reason: reason}
}

// baseOrderListLen returns the length of l, zero for nil.
func baseOrderListLen(l protoreflect.List) int {
	if l == nil {
		return 0
	}

	return l.Len()
}

// maskRefusal returns the refusal the check recorded, or the first limit
// the paths of work, the cleaned mask of md, break as a
// *fieldmask.ValidationError; nil otherwise. The limits bound the effective
// mask, not only the mask as sent: "*", a message named whole and a pinned
// "*" expand to the paths the update writes, which are checked as a request
// mask is, so the cleaned mask can be written back and merged
// ([MergePaths] checks the same limits).
func (u *baseOrderUpdateCheck) maskRefusal(work fieldmask.FieldMask, md protoreflect.MessageDescriptor) error {
	if u.refusal != nil {
		return u.refusal
	}

	return u.policy.validateMaskLimits(declaredPaths(work, md))
}

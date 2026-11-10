// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// Behavior sets skipped by the REQUIRED walks of an update. A replaced
// singular message keeps the stored value of an IMMUTABLE field the request
// leaves out, so only the fields a replacement writes are checked; the
// elements of a replaced collection are new values, checked as on create.
var (
	updateRequiredSkip = []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	}
	elementRequiredSkip = []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER}
)

// requiredMaxDepth bounds the REQUIRED walks of an update over the values it
// writes, as [fieldbehavior.WithMaxDepth] bounds them on create: a value
// nesting more messages is refused ([updateCheck.required]), never checked
// only in part.
const requiredMaxDepth = fieldbehavior.DefaultMaxDepth

// msgPair is a request message and its stored counterpart at the same path.
// Either is nil when absent: the request does not hold the message, the
// stored resource does not, or no stored resource was supplied. path is the
// concrete path of the message ("", "profile", "aliases.2", `labels.k`).
type msgPair struct {
	req, cur protoreflect.Message
	path     string
}

// topBehavior overrides the update behavior of a top-level field (see
// [Spec]): the behavior that governs it, or skip for a field an update
// neither writes nor checks.
type topBehavior func(fd protoreflect.FieldDescriptor) (b annotations.FieldBehavior, skip bool)

// updateCheck applies the AIP-203 rules of one update to its mask: it drops
// OUTPUT_ONLY entries, drops IMMUTABLE and IDENTIFIER entries whose value
// equals the stored one, expands a replaced message that holds server-managed
// fields to its other fields, and records a violation for every REQUIRED
// field the update leaves empty and every IMMUTABLE or IDENTIFIER value it
// would change or cannot verify.
type updateCheck struct {
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
	// refusal is the first refusal of the mask itself (a path error, not a
	// field behavior violation) the check made, nil otherwise.
	refusal error
	// expandVisits counts the messages [updateCheck.expand] visits in the
	// whole call, the validation work it bounds ([updateCheck.expandWithin]);
	// expandLeaves counts the leaves of the current top-level expansion
	// (expandNesting is zero outside one), which become paths of the mask
	// unless validationOnly says the expansion only compares protected
	// values.
	expandVisits, expandLeaves, expandNesting int
	validationOnly                            bool
	// atomicPaths caches [atomicProtectedPath] per atomic type.
	atomicPaths map[protoreflect.FullName]protectedPath
}

// newUpdateCheck returns the check of an update of req whose stored value is
// current (nil when unknown), and the root pair. A current message that is
// invalid or of another type than req is [ErrMessageMismatch].
func newUpdateCheck(req, current proto.Message, policy *options) (*updateCheck, msgPair, error) {
	u := &updateCheck{policy: policy}
	root := msgPair{req: req.ProtoReflect()}

	if current != nil {
		if _, _, err := reflectPair(req, current); err != nil {
			return nil, msgPair{}, err
		}

		u.verify = true
		root.cur = current.ProtoReflect()
	}

	return u, root, nil
}

// newApplyCheck returns the check of [Apply] and
// [ValidateStrict]: [newUpdateCheck] with the top-level fields
// governed by the mask policy as [Spec.Plan] governs them (see
// [policyTopBehavior]).
func newApplyCheck(req, current proto.Message, policy *options) (*updateCheck, msgPair, error) {
	u, root, err := newUpdateCheck(req, current, policy)
	if err != nil {
		return nil, msgPair{}, err
	}

	u.top = policyTopBehavior(policy)
	u.addressOnly = func(fd protoreflect.FieldDescriptor) bool { return isPolicyIdentity(fd, policy) }

	return u, root, nil
}

// policyTopBehavior is the [topBehavior] of the mask policy: a top-level
// field the policy names an identity field ([WithIdentityFields]) governs as
// IDENTIFIER and one it names immutable ([WithImmutableFields]) as
// IMMUTABLE, like the annotations; an OUTPUT_ONLY annotation dominates both.
func policyTopBehavior(policy *options) topBehavior {
	return func(fd protoreflect.FieldDescriptor) (annotations.FieldBehavior, bool) {
		managed := policy.serverManaged(fd)
		name := string(fd.Name())

		switch {
		case managed == annotations.FieldBehavior_OUTPUT_ONLY || managed == annotations.FieldBehavior_IDENTIFIER:
			return managed, false
		case slices.Contains(policy.identityFields, name):
			return annotations.FieldBehavior_IDENTIFIER, false
		case slices.Contains(policy.immutableFields, name):
			return annotations.FieldBehavior_IMMUTABLE, false
		default:
			return managed, false
		}
	}
}

// isPolicyIdentity reports whether the top-level field fd is an identity
// field of the policy only: named by [WithIdentityFields], neither annotated
// IDENTIFIER nor OUTPUT_ONLY. Such a field (id, etag by default) addresses
// the resource; the update keeps it but compares it with the stored value
// only when the mask names it, so a stale etag is left to the service's
// concurrency check (AIP-154).
func isPolicyIdentity(fd protoreflect.FieldDescriptor, policy *options) bool {
	return slices.Contains(policy.identityFields, string(fd.Name())) &&
		policy.serverManaged(fd) != annotations.FieldBehavior_OUTPUT_ONLY &&
		!slices.Contains(policy.behaviors(fd), annotations.FieldBehavior_IDENTIFIER)
}

// isAddressOnly reports whether fd, at the top level when topLevel, is one
// of u.addressOnly fields.
func (u *updateCheck) isAddressOnly(fd protoreflect.FieldDescriptor, topLevel bool) bool {
	return topLevel && u.addressOnly != nil && u.addressOnly(fd)
}

// behaviorOf returns the server-managed behavior governing fd at depth
// (top-level when prefix is empty) and whether the update skips it.
func (u *updateCheck) behaviorOf(fd protoreflect.FieldDescriptor, topLevel bool) (annotations.FieldBehavior, bool) {
	if topLevel && u.top != nil {
		return u.top(fd)
	}

	return u.policy.serverManaged(fd), false
}

// required returns the recorder of a REQUIRED walk of the value at path:
// it records the violations, or, when the walk exceeded
// [requiredMaxDepth], refuses the request with
// [fieldbehavior.ErrMaxDepthExceeded], since the REQUIRED fields below were
// not checked.
func (u *updateCheck) required(path string) func(v []FieldViolation, exceeded bool) {
	return func(v []FieldViolation, exceeded bool) {
		if exceeded {
			u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", path, requiredMaxDepth,
				fieldbehavior.ErrMaxDepthExceeded))

			return
		}

		u.violations = append(u.violations, v...)
	}
}

// fail records a violation.
func (u *updateCheck) fail(v FieldViolation) { u.violations = append(u.violations, v) }

// checkMask applies the update rules to msk, the sub-mask of message type md
// whose addressed messages are pairs, under the mask path prefix. It mutates
// msk: dropped entries are deleted, a branch left empty is deleted with them,
// and a replaced message holding server-managed fields becomes the sub-mask
// of its other fields.
func (u *updateCheck) checkMask(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []msgPair, prefix string) {
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
			u.checkMask(nested, fd.Message(), childPairs(pairs, fd), path)
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
func (u *updateCheck) unchanged(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string, b annotations.FieldBehavior,
) bool {
	if !u.verify {
		u.fail(behavior.ProtectedViolation(path, b, false))
		return false
	}

	for _, p := range pairs {
		if !sameSelection(fd, nested, p.req, p.cur) {
			u.fail(behavior.ProtectedViolation(path, b, true))
			return false
		}
	}

	return true
}

// verifyUnnamedIdentifiers compares the populated IDENTIFIER fields of md
// that msk does not name with the stored ones: the update keeps them to
// address the resource, so they must not differ from it. Without a stored
// resource they are accepted as the address.
func (u *updateCheck) verifyUnnamedIdentifiers(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []msgPair, prefix string) {
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
				u.fail(behavior.ProtectedViolation(behavior.JoinPath(prefix, string(fd.Name())), annotations.FieldBehavior_IDENTIFIER, true))
				break
			}
		}
	}
}

// checkWhole applies the rules to fd selected whole at path (entry name of
// msk): a REQUIRED field must be non-empty, a replacement must satisfy the
// REQUIRED fields of the value it writes, and a singular message holding a
// server-managed field is replaced through its other fields (see
// [updateCheck.expand]) so the stored server-managed values survive.
func (u *updateCheck) checkWhole(msk fieldmask.FieldMask, name string, fd protoreflect.FieldDescriptor, pairs []msgPair, path string) {
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

	if !u.policy.expandable(fd) || !hasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, u.policy) {
		return
	}

	onPath := map[protoreflect.FullName]bool{fd.ContainingMessage().FullName(): true, fd.Message().FullName(): true}
	if sub := u.expand(fd.Message(), childPairs(pairs, fd), path, onPath); len(sub) > 0 {
		msk[name] = sub
	} else {
		delete(msk, name)
	}
}

// isAtomic reports whether md is a message type the policy keeps whole
// ([WithAtomicMessage]); the dynamic Struct, Value and ListValue never are.
func (u *updateCheck) isAtomic(md protoreflect.MessageDescriptor) bool {
	return md != nil && u.policy.atomicMessage != nil && u.policy.atomicMessage(md) && !maskpath.IsDynamicMessage(md)
}

// atomicProtected is [atomicProtectedPath] of md, computed once per type for
// the check.
func (u *updateCheck) atomicProtected(md protoreflect.MessageDescriptor) (string, annotations.FieldBehavior) {
	if p, ok := u.atomicPaths[md.FullName()]; ok {
		return p.path, p.behavior
	}

	path, b := atomicProtectedPath(md, map[protoreflect.FullName]bool{}, u.policy)

	if u.atomicPaths == nil {
		u.atomicPaths = map[protoreflect.FullName]protectedPath{}
	}

	u.atomicPaths[md.FullName()] = protectedPath{path: path, behavior: b}

	return path, b
}

// protectedPath is the first field an atomic type must keep
// ([atomicProtectedPath]).
type protectedPath struct {
	path     string
	behavior annotations.FieldBehavior
}

// checkAtomicProtected checks fd, a singular message of an atomic type
// written whole in each pair, as a protected leaf: the merge copies the value
// whole, so it cannot keep a stored IMMUTABLE or IDENTIFIER field below the
// atomic boundary as an expansion keeps it in another message. When the type
// holds such a field ([atomicProtectedPath]), every one of them — reached
// through singular messages, a protected repeated or map field compared whole —
// must equal the stored one, absence included: the first change is a
// violation. Without the stored resource the replacement is one violation,
// since it cannot keep values it does not know (AIP-203).
func (u *updateCheck) checkAtomicProtected(fd protoreflect.FieldDescriptor, pairs []msgPair) {
	if !isAtomicValue(fd, u.policy) {
		return
	}

	first, b := u.atomicProtected(fd.Message())
	if first == "" {
		return
	}

	for _, p := range pairs {
		path := behavior.JoinPath(p.path, string(fd.Name()))

		if !u.verify {
			u.fail(behavior.ProtectedViolation(behavior.JoinPath(path, first), b, false))
			continue
		}

		if diff, b, ok := u.protectedDiff(fd.Message(), behavior.Child(p.req, fd), behavior.Child(p.cur, fd), path, 0); ok {
			u.fail(behavior.ProtectedViolation(diff, b, true))
		}
	}
}

// protectedDiff returns the path and behavior of the first protected field
// ([atomicProtectedPath]) of the message of type md whose request and stored
// values r and c (nil when absent) differ, at path prefix. It follows the
// values, only into sub-messages of a type that holds a protected field,
// within the REQUIRED walk depth and the shared work budget; a refusal
// stops it.
func (u *updateCheck) protectedDiff(md protoreflect.MessageDescriptor, r, c protoreflect.Message, prefix string, depth int,
) (string, annotations.FieldBehavior, bool) {
	if depth > requiredMaxDepth {
		u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", prefix, requiredMaxDepth,
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
func (u *updateCheck) checkWrittenRequired(fd protoreflect.FieldDescriptor, pairs []msgPair) {
	resolve := u.policy.behaviors

	for _, p := range pairs {
		if p.req == nil || !p.req.Has(fd) {
			continue
		}

		path := behavior.JoinPath(p.path, string(fd.Name()))

		switch {
		case fd.IsList():
			if fd.Kind() != protoreflect.MessageKind {
				continue
			}

			list := p.req.Get(fd).List()
			for i := range list.Len() {
				elemPath := behavior.JoinPath(path, strconv.Itoa(i))
				u.required(elemPath)(behavior.RequiredViolations(list.Get(i).Message(), elemPath, resolve, elementRequiredSkip, requiredMaxDepth))
			}
		case fd.IsMap():
			if fd.MapValue().Kind() != protoreflect.MessageKind {
				continue
			}

			mm := p.req.Get(fd).Map()
			for _, k := range behavior.SortedMapKeys(mm) {
				elemPath := behavior.JoinPath(path, behavior.QuoteSegment(k.String()))
				u.required(elemPath)(behavior.RequiredViolations(mm.Get(k).Message(), elemPath, resolve, elementRequiredSkip, requiredMaxDepth))
			}
		case fd.Message() != nil:
			skip := updateRequiredSkip
			if u.isAtomic(fd.Message()) {
				skip = elementRequiredSkip // an atomic value is written as sent, IMMUTABLE fields included
			}

			u.required(path)(behavior.ReplacedRequiredViolations(p.req.Get(fd).Message(), path, resolve, skip, elementRequiredSkip,
				requiredMaxDepth, nil, u.isAtomic))
		}
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
func (u *updateCheck) expand(md protoreflect.MessageDescriptor, pairs []msgPair, prefix string, onPath map[protoreflect.FullName]bool) fieldmask.FieldMask {
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

		if !u.policy.expandable(fd) || !hasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, u.policy) {
			u.checkAtomicProtected(fd, pairs)

			if !u.emitLeaf(path) {
				return nil
			}

			out[name] = nil

			continue
		}

		reqHas, curHas := pairsHold(pairs, fd)

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
		sub := u.expand(fd.Message(), childPairs(pairs, fd), path, onPath)

		if !wasOnPath {
			delete(onPath, child)
		}

		if len(sub) > 0 {
			out[name] = sub
		}
	}

	return out
}

// expandWithin counts a visit of [updateCheck.expand] to the message at
// prefix and reports whether the traversal stays within its own bounds,
// recording the refusal otherwise: the messages nested no deeper than the
// REQUIRED walks check ([requiredMaxDepth], refused with
// [fieldbehavior.ErrMaxDepthExceeded]) and the messages visited in the whole
// call within the work budget ([updateCheck.withinVisits]). These bound the
// traversal apart from the limits of the effective mask, which apply to the
// leaves it emits ([updateCheck.emitLeaf]) and to the mask as a whole
// afterwards. An acyclic schema that branches at every level would otherwise
// expand exponentially before the effective mask could be checked; the
// expansion stops at the first excess instead.
func (u *updateCheck) expandWithin(prefix string) bool {
	if u.refusal != nil {
		return false
	}

	u.expandVisits++

	// The traversal is bounded on its own: a message nesting deeper than the
	// REQUIRED walks check is refused, and so is work past the visit budget.
	// The path depth limit applies to the paths emitted ([updateCheck.emitLeaf]),
	// so a message at the limit whose fields are all protected expands to
	// nothing, as it emits no path.
	if u.expandNesting > requiredMaxDepth {
		u.refuse(fmt.Errorf("fieldmask: the request value %q nests more than %d messages: %w", prefix, requiredMaxDepth,
			fieldbehavior.ErrMaxDepthExceeded))

		return false
	}

	return u.withinVisits(prefix)
}

// emitLeaf counts the leaf at path [updateCheck.expand] emits and reports
// whether it fits the limits of the effective mask, recording the refusal
// otherwise: no deeper than [WithMaxPathDepth], and at most [WithMaxPaths]
// leaves per top-level expansion. A validation-only expansion emits no path
// and is held to neither.
func (u *updateCheck) emitLeaf(path string) bool {
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

// withinVisits reports whether the messages [updateCheck.expand] visited in
// the whole call stay within the validation work budget, recording the
// refusal otherwise. The work of every expansion — those that only compare
// protected values and those of covered paths included — is bounded apart
// from the effective mask, which is checked on its own: the larger of
// [WithMaxPaths] and [fieldmask.DefaultMaxPaths] times the path depth limit.
func (u *updateCheck) withinVisits(prefix string) bool {
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
func (u *updateCheck) writesOneofMember(fd protoreflect.FieldDescriptor, pairs []msgPair) bool {
	if od := fd.ContainingOneof(); od == nil || od.IsSynthetic() {
		return true
	}

	reqHas, curHas := pairsHold(pairs, fd)

	return reqHas || (u.verify && curHas)
}

// checkSetProtected compares the IMMUTABLE or IDENTIFIER field fd (behavior
// b) that a replacement or full update covers without naming it: unset in
// every request it keeps the stored value; set, it must equal the stored
// value. While the stored resource is unknown a set IMMUTABLE value is a
// violation and a set IDENTIFIER is accepted as the address.
func (u *updateCheck) checkSetProtected(fd protoreflect.FieldDescriptor, pairs []msgPair, path string, b annotations.FieldBehavior) {
	if reqHas, _ := pairsHold(pairs, fd); !reqHas {
		return
	}

	if !u.verify {
		if b == annotations.FieldBehavior_IMMUTABLE {
			u.fail(behavior.ProtectedViolation(path, b, false))
		}

		return
	}

	for _, p := range pairs {
		if p.req != nil && p.req.Has(fd) && !behavior.SameValue(p.req, p.cur, fd) {
			u.fail(behavior.ProtectedViolation(path, b, true))
			return
		}
	}
}

// replaceAll returns the mask of a full replacement ("*") of the root pair's
// message type md: [updateCheck.expand] of the root, after the REQUIRED
// fields of the request are checked as a replacement writes them. The
// replacement does not write a top-level field the update skips or governs
// as OUTPUT_ONLY, IDENTIFIER or IMMUTABLE — by annotation or by the policy
// ([WithImmutableFields], [WithIdentityFields]) — nor anything below it: an
// unset one keeps its stored value and a set one is compared, so its REQUIRED
// fields, and those below it, are not checked.
func (u *updateCheck) replaceAll(md protoreflect.MessageDescriptor, root msgPair) fieldmask.FieldMask {
	// A top-level field the replacement does not write is skipped before the
	// walk descends into it: its value, however deep, is compared by expand
	// below, never checked for REQUIRED fields it does not write.
	v, exceeded := behavior.ReplacedRequiredViolations(root.req, "", u.policy.behaviors, updateRequiredSkip, elementRequiredSkip, requiredMaxDepth,
		u.notWritten, u.isAtomic)
	if exceeded {
		u.required("")(nil, true)
		return nil
	}

	for _, violation := range v {
		u.fail(violation)
	}

	return u.expand(md, []msgPair{root}, "", map[protoreflect.FullName]bool{md.FullName(): true})
}

// notWritten reports whether a replacement leaves the top-level field fd to
// its stored value: one the update skips, or one it governs as OUTPUT_ONLY,
// IDENTIFIER or IMMUTABLE (see [updateCheck.expand]).
func (u *updateCheck) notWritten(fd protoreflect.FieldDescriptor) bool {
	if fd == nil {
		return false
	}

	b, skip := u.behaviorOf(fd, true)

	return skip || b == annotations.FieldBehavior_OUTPUT_ONLY || isImmutableBehavior(b) || u.isAddressOnly(fd, true)
}

// checkElements applies the rules to nested, the sub-mask of the repeated or
// map field fd, for the elements it addresses (see [elementMasks]): a
// sub-mask through a key or the "*" wildcard is checked against the element
// pairs it addresses, and an element named whole is a replacement whose
// REQUIRED fields are checked. A sub-mask emptied by the check is deleted
// once every element is checked: the elements "*" addresses are those no key
// named in the mask as sent, whichever key a check empties and whatever its
// spelling sorts before "*" ("", punctuation).
func (u *updateCheck) checkElements(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string) {
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

	for key, sub := range elementMasks(fd, named) {
		u.checkMask(sub, elem, elementPairs(fd, key, named, pairs), elementPath(path, key))

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
func (u *updateCheck) pinWildcard(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, named []string, pairs []msgPair, path string) {
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
				u.refuse(&fieldmask.ValidationError{Path: elementPath(path, maskpath.Wildcard), Reason: "a key path beside \"*\" that " +
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
		u.refuse(&fieldmask.ValidationError{Path: elementPath(path, maskpath.Wildcard), Reason: "a key path beside \"*\" that updates " +
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

// checkPinLimits refuses replacing the "*" sub-mask wildcard of the map at
// path by the keys before it is copied, when the generated paths would break
// a limit of the policy: more paths than [WithMaxPaths] (keys times the
// leaves of wildcard), a path longer than [WithMaxPathLength] or deeper than
// [WithMaxPathDepth]. The generated mask could neither be written back nor
// merged ([MergePaths] checks the same limits), so the mask is refused as a
// *fieldmask.ValidationError on the "*" path, matching the limit sentinel.
func (o *options) checkPinLimits(path string, keys []string, wildcard fieldmask.FieldMask) error {
	star := elementPath(path, maskpath.Wildcard)
	suffixes := wildcard.ToPaths()

	if n := len(keys) * len(suffixes); o.maxPaths > 0 && n > o.maxPaths {
		return &fieldmask.ValidationError{Path: star, Cause: fieldmask.ErrMaxPathsExceeded, Reason: fmt.Sprintf(
			"the \"*\" beside a key path that updates nothing expands to %d paths, more than the limit of %d; name the entries to update", n, o.maxPaths)}
	}

	for _, key := range keys {
		prefix := elementPath(path, key)
		for _, suffix := range suffixes {
			if v := o.limits().CheckPath(prefix + maskpath.Separator + suffix); v != nil {
				return &fieldmask.ValidationError{Path: star, Cause: v.Cause, Reason: "the \"*\" beside a key path that updates nothing expands to " + v.Reason}
			}
		}
	}

	return nil
}

// refuse records err, a refusal of the mask itself, unless one is recorded.
func (u *updateCheck) refuse(err error) {
	if u.refusal == nil {
		u.refusal = err
	}
}

// maskRefusal returns the refusal the check recorded, or the first limit
// the paths of work, the cleaned mask of md, break as a
// *fieldmask.ValidationError; nil otherwise. The limits bound the effective
// mask, not only the mask as sent: "*", a message named whole and a pinned
// "*" expand to the paths the update writes, which are checked as a request
// mask is, so the cleaned mask can be written back and merged
// ([MergePaths] checks the same limits).
func (u *updateCheck) maskRefusal(work fieldmask.FieldMask, md protoreflect.MessageDescriptor) error {
	if u.refusal != nil {
		return u.refusal
	}

	return u.policy.validateMaskLimits(declaredPaths(work, md))
}

// checkWholeElements records the REQUIRED fields left empty in the elements
// of fd that the sub-mask key of nested names whole: every element for "*"
// (the map entries no other key names), the entry of a map key.
func (u *updateCheck) checkWholeElements(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []msgPair) {
	for _, p := range elementPairs(fd, key, nested, pairs) {
		if p.req != nil {
			u.required(p.path)(behavior.RequiredViolations(p.req, p.path, u.policy.behaviors, elementRequiredSkip, requiredMaxDepth))
		}
	}
}

// elementPairs returns the element pairs of the repeated or map field fd that
// the element sub-mask under key of nested addresses in each pair's request:
// for a list every element ("*"), paired with the stored element of the
// same index; for a map with "*" every entry whose key no other sub-mask
// names, and for any other key its entry, paired with the stored entry of the
// same key. A map key the request does not hold still addresses the stored
// entry, with no request element, and a request without the list addresses
// every stored element, with none: the merge clears the fields the sub-path
// names in each.
func elementPairs(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []msgPair) []msgPair {
	var out []msgPair

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
				for i := range listLen(stored) {
					out = append(out, msgPair{cur: stored.Get(i).Message(), path: behavior.JoinPath(path, strconv.Itoa(i))})
				}

				continue
			}

			list := p.req.Get(fd).List()
			for i := range list.Len() {
				e := msgPair{req: list.Get(i).Message(), path: behavior.JoinPath(path, strconv.Itoa(i))}
				if stored != nil && i < stored.Len() {
					e.cur = stored.Get(i).Message()
				}

				out = append(out, e)
			}

			continue
		}

		out = append(out, mapElementPairs(fd, key, nested, p, path)...)
	}

	return out
}

// mapElementPairs is [elementPairs] for one pair p of a map field.
func mapElementPairs(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, p msgPair, path string) []msgPair {
	var reqMap, curMap protoreflect.Map
	if p.req != nil && p.req.Has(fd) {
		reqMap = p.req.Get(fd).Map()
	}

	if p.cur != nil && p.cur.Has(fd) {
		curMap = p.cur.Get(fd).Map()
	}

	entry := func(k protoreflect.MapKey) msgPair {
		e := msgPair{path: behavior.JoinPath(path, behavior.QuoteSegment(k.String()))}
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

		return []msgPair{entry(k)}
	}

	if reqMap == nil {
		return nil
	}

	var out []msgPair

	for _, k := range behavior.SortedMapKeys(reqMap) {
		if _, named := maskwalk.KeyMask(nested, k.String()); !named {
			out = append(out, entry(k))
		}
	}

	return out
}

// childPairs returns the pairs of the message field fd of each pair.
func childPairs(pairs []msgPair, fd protoreflect.FieldDescriptor) []msgPair {
	out := make([]msgPair, len(pairs))
	for i, p := range pairs {
		out[i] = msgPair{req: behavior.Child(p.req, fd), cur: behavior.Child(p.cur, fd), path: behavior.JoinPath(p.path, string(fd.Name()))}
	}

	return out
}

// pairsHold reports whether any request, and any stored message, of pairs
// populates fd.
func pairsHold(pairs []msgPair, fd protoreflect.FieldDescriptor) (reqHas, curHas bool) {
	for _, p := range pairs {
		reqHas = reqHas || (p.req != nil && p.req.Has(fd))
		curHas = curHas || (p.cur != nil && p.cur.Has(fd))
	}

	return reqHas, curHas
}

// sameSelection reports whether the part of field fd that the sub-mask nested
// selects (the whole field when nested is nil) is equal in r and c, either of
// which may be nil (holding nothing).
func sameSelection(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, r, c protoreflect.Message) bool {
	if nested == nil {
		return behavior.SameValue(r, c, fd)
	}

	tmpl := r
	if tmpl == nil {
		tmpl = c
	}

	if tmpl == nil {
		return true
	}

	selection := fieldmask.FieldMask{string(fd.Name()): nested}

	return proto.Equal(selectField(tmpl, fd, r, selection), selectField(tmpl, fd, c, selection))
}

// selectField returns a new message of tmpl's type holding a copy of field fd
// of m (nothing when m is nil), filtered by selection. Only the field is
// copied, never the rest of m.
func selectField(tmpl protoreflect.Message, fd protoreflect.FieldDescriptor, m protoreflect.Message, selection fieldmask.FieldMask) proto.Message {
	holder := tmpl.Type().New()
	if m != nil && m.Has(fd) {
		holder.Set(fd, m.Get(fd)) // shared until the clone below
	}

	msg := proto.Clone(holder.Interface())
	maskwalk.Filter(selection, msg, false)

	return msg
}

// clearOutputOnly clears every OUTPUT_ONLY value of msg at any depth,
// collection elements included: AIP-203 requires a service to clear them from
// a request. It runs after the request was filtered, so it only clears: the
// AIP-202 format checks of [fieldbehavior.Strip] are off (an update mask
// check reports no format violation), and nothing else can fail.
func clearOutputOnly(msg proto.Message, policy *options) error {
	return fieldbehavior.Strip(msg,
		fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY),
		fieldbehavior.WithResolver(policy.resolver),
		fieldbehavior.WithFieldFormats(false),
		fieldbehavior.WithMaxDepth(math.MaxInt))
}

// replaceMask makes msk hold exactly the entries of src.
func replaceMask(msk, src fieldmask.FieldMask) {
	clear(msk)
	maps.Copy(msk, src)
}

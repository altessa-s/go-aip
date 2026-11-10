// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
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
				v, _ := behavior.RequiredViolations(list.Get(i).Message(), behavior.JoinPath(path, strconv.Itoa(i)), resolve, elementRequiredSkip, math.MaxInt)
				u.violations = append(u.violations, v...)
			}
		case fd.IsMap():
			if fd.MapValue().Kind() != protoreflect.MessageKind {
				continue
			}

			mm := p.req.Get(fd).Map()
			for _, k := range behavior.SortedMapKeys(mm) {
				v, _ := behavior.RequiredViolations(mm.Get(k).Message(), behavior.JoinPath(path, behavior.QuoteSegment(k.String())), resolve,
					elementRequiredSkip, math.MaxInt)
				u.violations = append(u.violations, v...)
			}
		case fd.Message() != nil:
			u.violations = append(u.violations,
				behavior.ReplacedRequiredViolations(p.req.Get(fd).Message(), path, resolve, updateRequiredSkip, elementRequiredSkip, math.MaxInt)...)
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
			out[name] = nil
			continue
		}

		reqHas, curHas := pairsHold(pairs, fd)

		child := fd.Message().FullName()
		if (!reqHas && !curHas && u.verify) || (!reqHas && !curHas && onPath[child]) {
			continue
		}

		onPath[child] = true
		sub := u.expand(fd.Message(), childPairs(pairs, fd), path, onPath)
		delete(onPath, child)

		if len(sub) > 0 {
			out[name] = sub
		}
	}

	return out
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
// fields of the request are checked as a replacement writes them (a
// top-level field the update skips excepted).
func (u *updateCheck) replaceAll(md protoreflect.MessageDescriptor, root msgPair) fieldmask.FieldMask {
	v := behavior.ReplacedRequiredViolations(root.req, "", u.policy.behaviors, updateRequiredSkip, elementRequiredSkip, math.MaxInt)
	for _, violation := range v {
		if fd := md.Fields().ByName(protoreflect.Name(violation.Path)); fd != nil && u.top != nil {
			if _, skip := u.top(fd); skip {
				continue
			}
		}

		u.fail(violation)
	}

	return u.expand(md, []msgPair{root}, "", map[protoreflect.FullName]bool{md.FullName(): true})
}

// checkElements applies the rules to nested, the sub-mask of the repeated or
// map field fd, for the elements it addresses (see [elementMasks]): a
// sub-mask through a key or the "*" wildcard is checked against the element
// pairs it addresses, and an element named whole is a replacement whose
// REQUIRED fields are checked. A sub-mask emptied by the check is deleted.
func (u *updateCheck) checkElements(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string) {
	elem := wildcardElementDescriptor(fd, u.policy)
	if elem == nil {
		return
	}

	for _, key := range slices.Sorted(maps.Keys(nested)) {
		if nested[key] == nil && (fd.IsMap() || key == maskpath.Wildcard) {
			u.checkWholeElements(fd, key, nested, pairs)
		}
	}

	named := slices.Collect(maps.Keys(nested))

	for key, sub := range elementMasks(fd, nested) {
		u.checkMask(sub, elem, elementPairs(fd, key, nested, pairs), elementPath(path, key))

		if len(sub) == 0 {
			delete(nested, key)
		}
	}

	if fd.IsMap() {
		u.pinWildcard(fd, nested, named, pairs, path)
	}
}

// pinWildcard keeps the "*" sub-mask of nested, the sub-mask of the map
// field fd, off the entries other keys named (named, the keys before the
// check) when the check emptied the sub-mask of such a key: that entry was
// excluded from the "*" when it was checked, and without its key the "*"
// would select it on write. The "*" is then replaced by the keys of the
// entries it addresses, the request entries no other key names, each with a
// copy of its sub-mask, as [MergePaths] and the filter read it. The pairs
// share one mask, so a map below several elements cannot be pinned this
// way: such a mask is refused.
func (u *updateCheck) pinWildcard(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, named []string, pairs []msgPair, path string) {
	wildcard := nested[maskpath.Wildcard]
	if wildcard == nil || !slices.ContainsFunc(named, func(key string) bool {
		_, kept := nested[key]
		return key != maskpath.Wildcard && !kept
	}) {
		return
	}

	if len(pairs) != 1 {
		u.fail(FieldViolation{Path: elementPath(path, maskpath.Wildcard),
			Reason: "a key path beside \"*\" that updates nothing cannot be kept out of the wildcard here; name the entries to update"})

		return
	}

	delete(nested, maskpath.Wildcard)

	req := pairs[0].req
	if req == nil || !req.Has(fd) {
		return
	}

	mm := req.Get(fd).Map()
	for _, k := range behavior.SortedMapKeys(mm) {
		if key := k.String(); !slices.Contains(named, key) {
			nested[key] = wildcard.Clone()
		}
	}
}

// checkWholeElements records the REQUIRED fields left empty in the elements
// of fd that the sub-mask key of nested names whole: every element for "*"
// (the map entries no other key names), the entry of a map key.
func (u *updateCheck) checkWholeElements(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []msgPair) {
	for _, p := range elementPairs(fd, key, nested, pairs) {
		if p.req != nil {
			v, _ := behavior.RequiredViolations(p.req, p.path, u.policy.behaviors, elementRequiredSkip, math.MaxInt)
			u.violations = append(u.violations, v...)
		}
	}
}

// elementPairs returns the element pairs of the repeated or map field fd that
// the element sub-mask under key of nested addresses in each pair's request:
// for a list every element ("*"), paired with the stored element of the
// same index; for a map with "*" every entry whose key no other sub-mask
// names, and for any other key its entry, paired with the stored entry of the
// same key. A map key the request does not hold still addresses the stored
// entry, with no request element.
func elementPairs(fd protoreflect.FieldDescriptor, key string, nested fieldmask.FieldMask, pairs []msgPair) []msgPair {
	var out []msgPair

	for _, p := range pairs {
		path := behavior.JoinPath(p.path, string(fd.Name()))

		if fd.IsList() {
			if p.req == nil || !p.req.Has(fd) {
				continue
			}

			var stored protoreflect.List
			if p.cur != nil && p.cur.Has(fd) {
				stored = p.cur.Get(fd).List()
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
		if _, named := nested[k.String()]; !named {
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
// of m (nothing when m is nil), filtered by selection.
func selectField(tmpl protoreflect.Message, fd protoreflect.FieldDescriptor, m protoreflect.Message, selection fieldmask.FieldMask) proto.Message {
	out := tmpl.Type().New()
	if m != nil && m.Has(fd) {
		out.Set(fd, proto.Clone(m.Interface()).ProtoReflect().Get(fd))
	}

	msg := out.Interface()
	maskwalk.Filter(selection, msg, false)

	return msg
}

// clearOutputOnly clears every OUTPUT_ONLY value of msg at any depth,
// collection elements included: AIP-203 requires a service to clear them from
// a request.
func clearOutputOnly(msg proto.Message, policy *options) error {
	return fieldbehavior.Strip(msg,
		fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY),
		fieldbehavior.WithResolver(policy.resolver),
		fieldbehavior.WithMaxDepth(math.MaxInt))
}

// replaceMask makes msk hold exactly the entries of src.
func replaceMask(msk, src fieldmask.FieldMask) {
	clear(msk)
	maps.Copy(msk, src)
}

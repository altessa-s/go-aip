// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"maps"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// checkOneofSwitches refuses an update path whose chain sets a field of a
// oneof (a real one, not the synthetic oneof of a proto3 optional field)
// with another member the update must not clear: one that is IMMUTABLE or
// IDENTIFIER or holds such a field at any depth, and — for a top-level oneof —
// one the spec classifies as identity, immutable or secret (id, etag, a
// field named immutable on construction, an INPUT_ONLY value). Setting one
// member clears the others, so the merge could delete such a stored value
// without the mask naming it. With the stored resource cur (nil when
// unknown) only a member it holds is guarded; without it the check is
// conservative — by schema, whichever member is stored — so no mask mode
// (implied, "*" or explicit) can switch away from a protected branch. A
// member holding only OUTPUT_ONLY fields is not guarded: the server owns
// those values, and clearing them with the switch is not a client write.
func (s *Spec) checkOneofSwitches(paths []string, cur protoreflect.Message, explicit []maskPath) error {
	g := s.oneofGuard()
	for _, path := range paths {
		if sw := g.check(path, s.desc, cur); sw != nil {
			return locate(sw.maskPathError(), path, explicit)
		}
	}

	return nil
}

// oneofGuard returns the guard of the spec's classes: a top-level identity,
// immutable or secret field is guarded besides the annotated ones.
func (s *Spec) oneofGuard() oneofGuard {
	return oneofGuard{policy: s.opts, topGuarded: func(fd protoreflect.FieldDescriptor) bool {
		switch s.class(string(fd.Name())) {
		case classIdentity, classImmutable, classSecret:
			return true
		default:
			return false
		}
	}}
}

// policyOneofGuard returns the guard of the mask policy of [Apply],
// [ValidateStrict] and [ApplyMask]: a top-level field the policy names an
// identity ([WithIdentityFields]) or immutable ([WithImmutableFields]) field
// is guarded besides the annotated ones.
func policyOneofGuard(policy *options) oneofGuard {
	return oneofGuard{policy: policy, topGuarded: func(fd protoreflect.FieldDescriptor) bool {
		name := string(fd.Name())
		return slices.Contains(policy.identityFields, name) || slices.Contains(policy.immutableFields, name)
	}}
}

// oneofGuard refuses an update path that switches a oneof away from a member
// an update must not clear (see [Spec.checkOneofSwitches]).
type oneofGuard struct {
	policy *options
	// topGuarded reports whether a top-level field is guarded beyond its
	// field behaviors.
	topGuarded func(fd protoreflect.FieldDescriptor) bool
}

// oneofSwitch is an update path that would clear the guarded member other
// of its oneof.
type oneofSwitch struct {
	path  string
	other protoreflect.FieldDescriptor
}

func (sw *oneofSwitch) reason() string {
	return fmt.Sprintf("path %q would clear %q of its oneof, which an update must not change", sw.path, sw.other.Name())
}

// maskPathError is the refusal of sw by [Spec.Plan], located later.
func (sw *oneofSwitch) maskPathError() error {
	return &MaskPathError{Index: -1, Field: sw.path, Err: ErrProtectedField, reason: sw.reason()}
}

// violation is the refusal of sw by [Apply], [ValidateStrict] and
// [ApplyMask]: on the path, under the behavior of the guarded member
// (IMMUTABLE for a message holding a protected field).
func (sw *oneofSwitch) violation(policy *options) FieldViolation {
	b := policy.serverManaged(sw.other)
	if !isImmutableBehavior(b) {
		b = annotations.FieldBehavior_IMMUTABLE
	}

	return FieldViolation{Path: sw.path, Behavior: b, Reason: sw.reason()}
}

// check runs the guard on the update path path of md, spelled by declared
// names, against the stored resource cur (nil when unknown); a path that is
// not an update path is left to the other checks.
func (g oneofGuard) check(path string, md protoreflect.MessageDescriptor, cur protoreflect.Message) *oneofSwitch {
	segs, reason := resolveUpdateSegments(md, path, g.policy)
	if reason != "" {
		return nil
	}

	return g.checkPath(path, md, segs, cur, cur != nil, true)
}

// checkMask refuses the oneof switches the merge of msk, an update mask of md
// as [MergePaths] applies it, would make: it follows the merge targets with
// req, the request message at that level, and stored, the stored one (nil
// when absent; known reports that the stored resource is known). A field the
// request leaves unset is cleared, which switches no oneof and sets nothing
// below it, so only a field req holds is checked or descended: a oneof member
// it holds is checked against its siblings ([oneofGuard.checkSwitch]), a
// singular message is descended, the elements of a list under "*" are paired
// with the stored elements of the same index, and the entries of a map are
// those the merge updates — a key the mask names, or under "*" the request
// entries no key names — paired with the stored entries of the same key. A
// field named whole is not descended: a collection replaced whole holds new
// elements. prefix is the mask path of md, "*" kept as in the mask.
func (g oneofGuard) checkMask(
	msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, req, stored protoreflect.Message, known, topLevel bool, prefix string,
) *oneofSwitch {
	if req == nil || maskpath.IsDynamicMessage(md) {
		return nil
	}

	fields := md.Fields()
	for _, name := range slices.Sorted(maps.Keys(msk)) {
		fd := fields.ByName(protoreflect.Name(name))
		if fd == nil || !req.Has(fd) {
			continue
		}

		path := behavior.JoinPath(prefix, name)

		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			if sw := g.checkSwitch(path, fd, od, topLevel, stored, known); sw != nil {
				return sw
			}
		}

		nested := msk[name]

		var sw *oneofSwitch

		switch {
		case nested == nil:
		case fd.IsList():
			sw = g.checkList(fd, nested[maskpath.Wildcard], req, stored, known, path)
		case fd.IsMap():
			sw = g.checkMap(fd, nested, req, stored, known, path)
		case fd.Message() != nil:
			sw = g.checkMask(nested, fd.Message(), req.Get(fd).Message(), behavior.Child(stored, fd), known, false, path)
		}

		if sw != nil {
			return sw
		}
	}

	return nil
}

// checkList runs [oneofGuard.checkMask] with sub, the "*" sub-mask of the
// repeated field fd, on every element req holds, paired with the stored
// element of the same index.
func (g oneofGuard) checkList(
	fd protoreflect.FieldDescriptor, sub fieldmask.FieldMask, req, stored protoreflect.Message, known bool, path string,
) *oneofSwitch {
	elem := maskpath.ValueMessage(fd)
	if sub == nil || elem == nil {
		return nil
	}

	list := req.Get(fd).List()
	storedList := listOf(stored, fd)

	for i := range list.Len() {
		var cur protoreflect.Message
		if storedList != nil && i < storedList.Len() {
			cur = storedList.Get(i).Message()
		}

		if sw := g.checkMask(sub, elem, list.Get(i).Message(), cur, known, false, elementPath(path, maskpath.Wildcard)); sw != nil {
			return sw
		}
	}

	return nil
}

// checkMap runs [oneofGuard.checkMask] on the entries of the map field fd
// the merge of nested updates in place: the request entry of every key
// nested names with a sub-mask, and under "*" every request entry no key
// names, each paired with the stored entry of the same key.
func (g oneofGuard) checkMap(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, req, stored protoreflect.Message, known bool, path string,
) *oneofSwitch {
	elem := maskpath.ValueMessage(fd)
	if elem == nil {
		return nil
	}

	reqMap, storedMap := req.Get(fd).Map(), mapOf(stored, fd)
	entry := func(m protoreflect.Map, k protoreflect.MapKey) protoreflect.Message {
		if m == nil {
			return nil
		}

		if v := m.Get(k); v.IsValid() {
			return v.Message()
		}

		return nil
	}

	for _, key := range slices.Sorted(maps.Keys(nested)) {
		sub := nested[key]
		if sub == nil || key == maskpath.Wildcard {
			continue
		}

		k, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), key)
		if !ok {
			continue
		}

		if sw := g.checkMask(sub, elem, entry(reqMap, k), entry(storedMap, k), known, false, elementPath(path, key)); sw != nil {
			return sw
		}
	}

	wildcard := nested[maskpath.Wildcard]
	if wildcard == nil {
		return nil
	}

	for _, k := range behavior.SortedMapKeys(reqMap) {
		if _, named := maskwalk.KeyMask(nested, k.String()); named {
			continue
		}

		if sw := g.checkMask(wildcard, elem, entry(reqMap, k), entry(storedMap, k), known, false, elementPath(path, maskpath.Wildcard)); sw != nil {
			return sw
		}
	}

	return nil
}

// checkOneofPath runs [oneofGuard.checkSwitch] on every field of the
// update path segs of md (path in full) whose stored message is stored;
// known reports that the stored resource is known and topLevel that md is
// the resource. Below a map key the stored entry of that key is checked,
// below "*" every stored element or entry; without the stored resource the
// check is by schema.
func (g oneofGuard) checkPath(
	path string, md protoreflect.MessageDescriptor, segs []string, stored protoreflect.Message, known, topLevel bool,
) *oneofSwitch {
	if maskpath.IsDynamicMessage(md) {
		return nil // dynamic JSON: keys, not fields
	}

	fd := md.Fields().ByName(protoreflect.Name(segs[0]))
	if fd == nil {
		return nil
	}

	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		if sw := g.checkSwitch(path, fd, od, topLevel, stored, known); sw != nil {
			return sw
		}
	}

	switch {
	case len(segs) == 1:
		return nil
	case fd.IsList() || fd.IsMap():
		key, rest := segs[1], segs[2:]
		if len(rest) == 0 {
			return nil
		}

		elem := maskpath.ValueMessage(fd)
		if !known {
			return g.checkPath(path, elem, rest, nil, false, false)
		}

		for _, e := range storedElements(fd, key, stored) {
			if sw := g.checkPath(path, elem, rest, e, true, false); sw != nil {
				return sw
			}
		}

		return nil
	default:
		return g.checkPath(path, fd.Message(), segs[1:], behavior.Child(stored, fd), known, false)
	}
}

// storedElements returns the element messages of the repeated or map field
// fd of stored (nil when absent) that key addresses: every element for "*",
// the entry of a map key otherwise (none when stored lacks it).
func storedElements(fd protoreflect.FieldDescriptor, key string, stored protoreflect.Message) []protoreflect.Message {
	if stored == nil || !stored.Has(fd) {
		return nil
	}

	var out []protoreflect.Message

	if fd.IsList() {
		list := stored.Get(fd).List()
		for i := range list.Len() {
			out = append(out, list.Get(i).Message())
		}

		return out
	}

	mm := stored.Get(fd).Map()
	if key != maskpath.Wildcard {
		if k, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), key); ok {
			if v := mm.Get(k); v.IsValid() {
				out = append(out, v.Message())
			}
		}

		return out
	}

	for _, k := range behavior.SortedMapKeys(mm) {
		out = append(out, mm.Get(k).Message())
	}

	return out
}

// checkSwitch refuses setting fd, a member of od at the chain depth
// whose stored message is stored, when another member is guarded (see
// [Spec.checkOneofSwitches]); known reports that the stored resource is
// known, so stored (nil when absent) tells which member it holds.
func (g oneofGuard) checkSwitch(
	path string,
	fd protoreflect.FieldDescriptor,
	od protoreflect.OneofDescriptor,
	topLevel bool,
	stored protoreflect.Message,
	known bool,
) *oneofSwitch {
	members := od.Fields()
	for i := range members.Len() {
		other := members.Get(i)
		if other.Number() == fd.Number() || !g.guarded(other, topLevel) {
			continue
		}

		if known && (stored == nil || !stored.Has(other)) {
			continue
		}

		return &oneofSwitch{path: path, other: other}
	}

	return nil
}

// guarded reports whether an update must not clear fd as a side effect: an
// IMMUTABLE or IDENTIFIER field or a message holding one at any depth, and,
// for a top-level field, one topGuarded reports. An OUTPUT_ONLY field is
// never guarded, whatever it holds or the policy names it, as OUTPUT_ONLY
// governs a field in every check of the update.
func (g oneofGuard) guarded(fd protoreflect.FieldDescriptor, topLevel bool) bool {
	policy := g.policy

	switch policy.serverManaged(fd) { //nolint:exhaustive // serverManaged returns only these or UNSPECIFIED.
	case annotations.FieldBehavior_OUTPUT_ONLY:
		// OUTPUT_ONLY dominates the other behaviors, its descendants and the
		// policy names: the server owns the value, and clearing it with the
		// switch is not a client write.
		return false
	case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
		return true
	}

	if md := maskpath.ValueMessage(fd); md != nil {
		switch {
		case isProtectedAtomicType(md, policy):
			return true // a protected leaf: switching away from it clears it whole
		case policy.atomicMessage != nil && policy.atomicMessage(md):
		case holdsImmutable(md, map[protoreflect.FullName]bool{}, policy):
			return true
		}
	}

	return topLevel && g.topGuarded != nil && g.topGuarded(fd)
}

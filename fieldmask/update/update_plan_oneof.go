// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
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
	for _, path := range paths {
		segs, reason := resolveUpdateSegments(s.desc, path, s.opts)
		if reason != "" {
			continue
		}

		if err := s.checkOneofPath(path, s.desc, segs, cur, cur != nil, true); err != nil {
			return locate(err, path, explicit)
		}
	}

	return nil
}

// checkOneofPath runs [Spec.checkOneofSwitch] on every field of the
// update path segs of md (path in full) whose stored message is stored;
// known reports that the stored resource is known and topLevel that md is
// the resource. Below a map key the stored entry of that key is checked,
// below "*" every stored element or entry; without the stored resource the
// check is by schema.
func (s *Spec) checkOneofPath(path string, md protoreflect.MessageDescriptor, segs []string, stored protoreflect.Message, known, topLevel bool) error {
	if maskpath.IsDynamicMessage(md) {
		return nil // dynamic JSON: keys, not fields
	}

	fd := md.Fields().ByName(protoreflect.Name(segs[0]))
	if fd == nil {
		return nil
	}

	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		if err := s.checkOneofSwitch(path, fd, od, topLevel, stored, known); err != nil {
			return err
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
			return s.checkOneofPath(path, elem, rest, nil, false, false)
		}

		for _, e := range storedElements(fd, key, stored) {
			if err := s.checkOneofPath(path, elem, rest, e, true, false); err != nil {
				return err
			}
		}

		return nil
	default:
		return s.checkOneofPath(path, fd.Message(), segs[1:], behavior.Child(stored, fd), known, false)
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

// checkOneofSwitch refuses setting fd, a member of od at the chain depth
// whose stored message is stored, when another member is guarded (see
// [Spec.checkOneofSwitches]); known reports that the stored resource is
// known, so stored (nil when absent) tells which member it holds.
func (s *Spec) checkOneofSwitch(
	path string,
	fd protoreflect.FieldDescriptor,
	od protoreflect.OneofDescriptor,
	topLevel bool,
	stored protoreflect.Message,
	known bool,
) error {
	members := od.Fields()
	for i := range members.Len() {
		other := members.Get(i)
		if other.Number() == fd.Number() || !s.guarded(other, topLevel) {
			continue
		}

		if known && (stored == nil || !stored.Has(other)) {
			continue
		}

		return &MaskPathError{Index: -1, Field: path, Err: ErrProtectedField, reason: fmt.Sprintf(
			"path %q would clear %q of its oneof, which an update must not change", path, other.Name())}
	}

	return nil
}

// guarded reports whether an update must not clear fd as a side effect: an
// IMMUTABLE or IDENTIFIER field or a message holding one at any depth, and,
// for a top-level field, one the spec classifies as identity, immutable or
// secret.
func (s *Spec) guarded(fd protoreflect.FieldDescriptor, topLevel bool) bool {
	policy := s.opts
	if isImmutableBehavior(policy.serverManaged(fd)) {
		return true
	}

	if md := maskpath.ValueMessage(fd); md != nil && (policy.atomicMessage == nil || !policy.atomicMessage(md)) &&
		holdsImmutable(md, map[protoreflect.FullName]bool{}, policy) {
		return true
	}

	if !topLevel {
		return false
	}

	switch s.class(string(fd.Name())) {
	case classIdentity, classImmutable, classSecret:
		return true
	default:
		return false
	}
}

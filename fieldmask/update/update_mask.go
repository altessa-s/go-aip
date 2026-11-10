// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"iter"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// FieldViolation describes a single field-behavior constraint that was violated
// during [Apply]. Path is the dot-separated path, Behavior
// is the google.api.field_behavior value that triggered the violation, and
// Reason explains the rejection in human-readable form. Format is always
// empty: an update mask check reports no AIP-202 format violation.
//
// FieldViolation is an alias for [behavior.Violation] so the gRPC interceptor
// can render fieldmask and fieldbehavior violations through the same
// io.altessa.badrequest.v1.BadRequest FieldViolation mapping.
type FieldViolation = behavior.Violation

// BehaviorError is returned by [Apply] when the
// update breaks a google.api.field_behavior rule (AIP-203): a REQUIRED field
// left empty, or an IMMUTABLE or IDENTIFIER value changed or not verifiable.
// Inspect Violations for per-field details. It maps to INVALID_ARGUMENT.
//
// Distinct from [fieldbehavior.BehaviorViolationError], which reports the
// payload checks of the fieldbehavior package; both carry [FieldViolation]
// values.
type BehaviorError struct {
	Violations []FieldViolation // One entry per violated field.
}

// Error lists the violations.
func (e *BehaviorError) Error() string {
	var buf strings.Builder
	buf.WriteString("field behavior violation: ")

	for i, v := range e.Violations {
		if i > 0 {
			buf.WriteString("; ")
		}

		if v.Path != "" { // a mask-level violation names no path
			buf.WriteString(v.Path)
			buf.WriteString(": ")
		}

		buf.WriteString(v.Reason)
	}

	return buf.String()
}

// Apply applies the field mask of an update request (AIP-134) to its
// resource msg, enforcing the field behaviors of AIP-203. Pass the stored
// resource with [WithCurrent]: only then can an unchanged IMMUTABLE or
// IDENTIFIER value be told from a change. The steps:
//
//  1. "*" alone is a full replacement: it becomes the mask of every field but
//     the OUTPUT_ONLY ones (see the replacement rule below), and the
//     REQUIRED fields of msg are checked as for a replaced message. "*" with
//     other paths is a *fieldmask.ValidationError.
//  2. Every path is resolved against msg's schema, so a segment may use any
//     spelling of its field ([fieldmask.FieldMask.Resolve]: declared, JSON or the
//     snake_case form protojson decodes a JSON path to); the cleaned mask
//     spells it by its declared name. With [WithPathValidation] set to
//     [PathValidationEnforce], a path absent from the schema is rejected
//     (fail-fast); [PathValidationWarn] reports it and continues, and the
//     path then matches no field. Indexed list paths ("aliases.0.id",
//     "list_data.0") and, with [WithStrictDynamicPaths] in the update policy,
//     paths below a google.protobuf.Struct key or into a Value or ListValue
//     ("attrs.color.hue") are always rejected. Every path is checked on its
//     own: one that another path of msk covers once both are resolved (a
//     parent spelled by its JSON name, "backupOwner" with
//     "backup_owner.org") is checked in step 3 as if sent alone, though only
//     the covering path is applied.
//  3. The field behaviors are applied to the mask, at any depth:
//     - OUTPUT_ONLY: the path is dropped from the mask, never an error.
//     - IMMUTABLE and IDENTIFIER: with [WithCurrent], a path whose value
//     equals the stored one is dropped (ignored); a changed value is a
//     violation. Without it the stored value is unknown and every such path
//     is a violation, so a change is never applied or lost silently.
//     - REQUIRED: a path naming a REQUIRED field whose value is absent or
//     falsy (empty string or bytes, zero, false, zero enum, empty list or
//     map, unset message) is a violation; a REQUIRED field absent from the
//     mask may be omitted from msg.
//     - The populated IDENTIFIER fields the mask does not name address the
//     resource; with [WithCurrent] one that differs from the stored value is
//     a violation.
//  4. On any violation msk and msg are left untouched and
//     *BehaviorError is returned. When no path is left the update
//     is a no-op (see below). Otherwise the set IDENTIFIER
//     fields of the masked branches are added to the mask so they survive
//     the filter, fields NOT in the mask are cleared, a field IN the mask
//     that msg leaves unset stays unset (the update clears it: a message or
//     an optional field gets no presence, a default is never written), and
//     every OUTPUT_ONLY value left in msg (inside a collection replaced
//     whole, say) is cleared.
//
// The mask policy of the options governs the top-level fields as it
// governs [Spec.Plan]: a field named by [WithImmutableFields] is
// IMMUTABLE, and a field named by [WithIdentityFields] (id and etag by
// default) is IDENTIFIER when the mask names it — an unchanged value is
// ignored, a changed one, or any one without [WithCurrent], is a violation.
// Unnamed, such an identity field addresses the resource: it is kept in msg
// and added to the mask like an IDENTIFIER, but not compared with the stored
// value (a stale etag is the service's concurrency check, AIP-154). An
// OUTPUT_ONLY annotation dominates both lists.
//
// Naming a singular message field whole (no sub-path) replaces it. The
// REQUIRED fields of the populated value are checked, recursing only into
// populated sub-messages. When the message holds a server-managed field —
// OUTPUT_ONLY, IDENTIFIER or IMMUTABLE, directly or in a singular sub-message
// at any depth — the entry is expanded to the message's other fields so the
// stored server-managed values survive the replacement: OUTPUT_ONLY fields
// are left out, an IMMUTABLE or IDENTIFIER field msg leaves unset is left out
// (it keeps its stored value) and one msg sets is compared as above (an unset
// IDENTIFIER without [WithCurrent] is kept as the address). A sub-message that
// neither msg nor the stored resource holds is left out; without
// [WithCurrent], a recursive sub-message msg does not hold is left as stored.
//
// A repeated or map field named whole, or one of its elements named whole
// ("labels.k", "aliases.*"), is replaced as a whole: its elements are new
// values. Their REQUIRED fields are checked as on create, their OUTPUT_ONLY
// values are cleared, and their IMMUTABLE and IDENTIFIER values are the first
// values of new elements, neither compared nor rejected. A sub-path into the
// elements — through a map key ("labels.k.display_name") or the "*" wildcard
// ("aliases.*.display_name") — updates each element it addresses in place, so
// the rules above apply per element: a list element is compared with the
// stored element of the same index, a map entry with the stored entry of the
// same key. Each element such a sub-path addresses keeps its set IDENTIFIER
// fields, as a singular sub-message does. A "*" naming the elements whole
// ("delegates.*") covers every key path beside it ("delegates.k.org"),
// which is checked as if sent alone, as [Spec.Plan] reads it. A "*" sub-path
// of a map ("delegates.*.email") addresses the entries no key path names;
// when the check drops such a key path (an unchanged IMMUTABLE value, say),
// the "*" is replaced by the keys of the entries it addresses, so the cleaned
// mask never selects the entry that was left out.
//
// msk keeps the presence of the update_mask (AIP-134), as
// [fieldmask.FromProtoFieldMask] builds it:
//   - A nil msk is an omitted update_mask: it applies the implied mask,
//     [FromSetFields] of msg under the options, as if passed
//     instead. msk cannot report it; build that mask yourself to see
//     what was applied.
//   - A non-nil empty msk is an update_mask sent with no paths: a no-op.
//     msg and msk are left untouched and no field behavior is
//     checked; only a [WithCurrent] message that is invalid or of another
//     type is still [ErrMessageMismatch], as for [Spec.Plan].
//   - "*" alone is a full replacement (step 1).
//
// A mask whose every path is dropped (OUTPUT_ONLY paths, unchanged IMMUTABLE
// or IDENTIFIER values) updates nothing either: once its checks pass, the
// msk is emptied and msg is left untouched. So, mirroring
// [Plan.Empty], [fieldmask.FieldMask.IsEmpty] on a non-nil msk after a
// successful call reports that the update changes nothing; msg then holds the
// request as sent and must not be stored as the resource.
//
// Returns *fieldmask.ValidationError for a rejected path, *BehaviorError for
// field behavior violations, and [ErrMessageMismatch] when the [WithCurrent]
// message is invalid or not of msg's type.
//
// This ensures the service can distinguish:
//   - "don't update this field" (field not in mask)
//   - "clear this field" (field in mask, absent from msg)
//   - "update this field" (field in mask, set to value)
//
// On success it mutates msk: paths are respelled by declared names,
// dropped entries are removed, expanded entries replaced and IDENTIFIER
// leaves of msg added, so the cleaned msk reflects what was actually applied.
// Callers that want to reuse the original mask should pass [fieldmask.FieldMask.Clone]
// of it.
func Apply(msk fieldmask.FieldMask, msg proto.Message, opts ...Option) error {
	o := newOptions(opts...)
	policy := o

	if msg == nil {
		return nil
	}

	u, root, err := newApplyCheck(msg, o.current, policy)
	if err != nil {
		return err
	}

	switch {
	case msk == nil:
		return Apply(impliedUpdateMask(msg, o), msg, opts...)
	case len(msk) == 0:
		return nil // a provided empty update_mask is a no-op (AIP-134).
	}

	descriptor := msg.ProtoReflect().Descriptor()

	all, err := msk.SelectsAll()
	if err != nil {
		return &fieldmask.ValidationError{Path: maskpath.Wildcard, Reason: maskpath.ReasonWildcardNotAlone}
	}

	var work fieldmask.FieldMask

	if all {
		work = u.replaceAll(descriptor, root)
	} else {
		var covered []fieldmask.FieldMask

		work, covered, err = resolveUpdatePaths(msk.ToPaths(), descriptor, policy, o)
		if err != nil {
			return err
		}

		u.checkCovering(work, covered, descriptor, root)
	}

	if len(u.violations) > 0 {
		return &BehaviorError{Violations: u.violations}
	}

	replaceMask(msk, work)

	if len(work) == 0 {
		return nil // every path was dropped: nothing to update.
	}

	preserveIdentifierFields(msk, msg, policy)
	maskwalk.Filter(msk, msg, false)

	return clearOutputOnly(msg, policy)
}

// impliedUpdateMask returns the implied mask of an omitted update_mask,
// [FromSetFields] of msg under policy, never nil so that applying
// it cannot recurse into the omitted case again.
func impliedUpdateMask(msg proto.Message, policy *options) fieldmask.FieldMask {
	if implied := fromSetFields(msg, policy); implied != nil {
		return implied
	}

	return fieldmask.FieldMask{}
}

// resolveUpdatePaths returns the mask of paths spelled by declared names
// (see [resolveUpdatePath]) after the path checks of [Apply]: schema
// validation as o configures it, indexed list access, and atomic messages
// selected whole. A path the schema rejects is kept as written when o lets
// it through: it matches no field. Every path is checked on its own before
// the mask collapses the paths another one covers ("profile.id" under
// "profile", or under "profile" spelled otherwise), which come back as the
// single-path masks covered for the field behavior check
// ([updateCheck.checkCovering]).
func resolveUpdatePaths(paths []string, md protoreflect.MessageDescriptor, policy *options, o *options) (
	work fieldmask.FieldMask, covered []fieldmask.FieldMask, err error,
) {
	if err := policy.validateMaskLimits(paths); err != nil {
		return nil, nil, err // limits hold in every PathValidationMode
	}

	resolved := make([][]string, 0, len(paths))

	for _, path := range paths {
		name, err := maskpath.ResolvePath(md, path, policy.strictDynamicPaths)
		if err != nil {
			err = validationError(err)
			name = path // kept as written: it matches no field
		}

		if err != nil && o.shouldValidatePaths() {
			if o.pathValidation == PathValidationEnforce {
				return nil, nil, err // fail-fast on the first schema-invalid path.
			}
			// PathValidationWarn: report every bad path but still apply the
			// mask. shouldValidatePaths guarantees a non-nil reporter here.
			if validationErr, ok := errors.AsType[*fieldmask.ValidationError](err); ok {
				o.pathReporter(validationErr)
			}
		}

		if err := maskpath.RejectStructural(md, name, policy.strictDynamicPaths); err != nil {
			return nil, nil, validationError(err)
		}

		resolved = append(resolved, maskpath.Split(name))
	}

	work, covered = coverPaths(resolved)
	for _, m := range slices.Concat([]fieldmask.FieldMask{work}, covered) {
		if err := policy.checkAtomicMask(m, md, ""); err != nil {
			return nil, nil, err
		}
	}

	return work, covered, nil
}

// coverPaths returns the mask of the split paths and, for every path another
// one covers in that mask, the mask of that path alone: the mask holds the
// covering path only, so the covered one must be checked on its own. A path
// is covered by a shorter path ("profile.id" by "profile") and, in a
// collection whose elements "*" names whole ("delegates.*"), by that "*": a
// key path beside it ("delegates.k.org") is removed from the mask, so the
// elements "*" replaces are exactly those it was checked for, as
// [Spec.Plan] and [MergePaths] read "delegates.*". An exact duplicate is not
// covered.
func coverPaths(paths [][]string) (fieldmask.FieldMask, []fieldmask.FieldMask) {
	work := fieldmask.FieldMask{}
	for _, parts := range paths {
		maskwalk.Insert(work, parts)
	}

	pruneWildcardSiblings(work)

	var covered []fieldmask.FieldMask

	for _, parts := range paths {
		if isCovered(work, parts) {
			single := fieldmask.FieldMask{}
			maskwalk.Insert(single, parts)
			covered = append(covered, single)
		}
	}

	return work, covered
}

// pruneWildcardSiblings removes, at every level of msk, the keys beside a
// "*" that names every element whole: that "*" already replaces them.
func pruneWildcardSiblings(msk fieldmask.FieldMask) {
	if sub, ok := msk[maskpath.Wildcard]; ok && sub == nil {
		for key := range msk {
			if key != maskpath.Wildcard {
				delete(msk, key)
			}
		}

		return
	}

	for _, nested := range msk {
		if nested != nil {
			pruneWildcardSiblings(nested)
		}
	}
}

// isCovered reports whether the path of parts, inserted into msk, is no
// longer held by it as written: a shorter path or a whole-element "*" beside
// it ([pruneWildcardSiblings]) covers it.
func isCovered(msk fieldmask.FieldMask, parts []string) bool {
	level := msk

	for i, seg := range parts {
		nested, ok := level[seg]
		switch {
		case !ok:
			return true // removed beside a whole-element "*"
		case i == len(parts)-1:
			return false
		case nested == nil:
			return true // under a shorter path
		}

		level = nested
	}

	return false
}

// checkCovering applies the update rules to work, the mask of an update's
// paths of message type md, and then to each of covered, the single-path
// masks of the paths work covers ([coverPaths]): a covered path is checked
// as if sent alone ("profile.id" under "profile" names an IDENTIFIER), but
// only work is applied. A violation both report is recorded once.
func (u *updateCheck) checkCovering(work fieldmask.FieldMask, covered []fieldmask.FieldMask, md protoreflect.MessageDescriptor, root msgPair) {
	u.checkMask(work, md, []msgPair{root}, "")

	if len(covered) == 0 {
		return
	}

	for _, m := range covered {
		u.checkMask(m, md, []msgPair{root}, "")
	}

	seen := make(map[FieldViolation]bool, len(u.violations))
	u.violations = slices.DeleteFunc(u.violations, func(v FieldViolation) bool {
		dup := seen[v]
		seen[v] = true

		return dup
	})
}

// resolveLenient returns msk with every path md's schema accepts spelled by
// the declared names of its fields ([maskpath.ResolvePath]) and every other path
// as written.
func resolveLenient(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, strict bool) fieldmask.FieldMask {
	paths := msk.ToPaths()
	for i, path := range paths {
		if name, err := maskpath.ResolvePath(md, path, strict); err == nil {
			paths[i] = name
		}
	}

	return fieldmask.FromPaths(paths...)
}

// preserveIdentifierFields adds set IDENTIFIER fields (AIP-203) to the mask as
// leaf entries so [maskwalk.Filter] does not clear them. The identifier
// is the resource's routing key — callers normally pass it alongside the
// update_mask without listing it in the mask itself, and dropping it would
// silently break the update path. Only set fields are added: if the caller did
// not provide the identifier, this step is a no-op and downstream logic is
// untouched.
// An IDENTIFIER that is also OUTPUT_ONLY is not preserved: the update never
// writes an OUTPUT_ONLY field.
//
// The step descends every branch of the mask: singular sub-messages and the
// elements of repeated and map fields a sub-path addresses (see
// [preserveElementIdentifiers]), so an element keeps its identifier as a
// sub-message does. Leaves — whole messages, elements and collections — keep
// their whole value anyway and are not descended. At the top level the
// identity fields of the policy ([WithIdentityFields]) are kept too: they
// address the resource as an IDENTIFIER does.
func preserveIdentifierFields(msk fieldmask.FieldMask, msg proto.Message, policy *options) {
	prf := msg.ProtoReflect()
	ids := identifierFields(prf.Descriptor(), policy)

	fields := prf.Descriptor().Fields()
	for _, name := range policy.identityFields {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil && isPolicyIdentity(fd, policy) {
			ids = append(ids, fd)
		}
	}

	preserveIdentifiers(msk, prf, ids, policy)
}

// preserveIdentifiers is [fieldmask.FieldMask.preserveIdentifierFields] on prf, with
// ids the [identifierFields] of its type, resolved once by the caller for all
// the elements of a collection.
func preserveIdentifiers(msk fieldmask.FieldMask, prf protoreflect.Message, ids []protoreflect.FieldDescriptor, policy *options) {
	for _, fd := range ids {
		if name := string(fd.Name()); prf.Has(fd) {
			if _, exists := msk[name]; !exists {
				msk[name] = nil
			}
		}
	}
	// Recurse into the branches already in the mask so identifiers within
	// explicitly-updated subtrees are also preserved. Branches into
	// server-managed fields are gone by now: OUTPUT_ONLY ones were removed,
	// the others rejected.
	fields := prf.Descriptor().Fields()

	for name, nested := range msk {
		if nested == nil {
			continue
		}

		fd := fields.ByName(protoreflect.Name(name))
		if fd == nil || !prf.Has(fd) {
			continue
		}

		switch {
		case fd.IsList() || fd.IsMap():
			preserveElementIdentifiers(fd, prf.Get(fd), nested, policy)
		case policy.expandable(fd):
			sub := prf.Get(fd).Message()
			preserveIdentifiers(nested, sub, identifierFields(sub.Descriptor(), policy), policy)
		}
	}
}

// identifierFields returns the IDENTIFIER fields of md that
// [fieldmask.FieldMask.preserveIdentifierFields] keeps: those not also OUTPUT_ONLY.
func identifierFields(md protoreflect.MessageDescriptor, policy *options) []protoreflect.FieldDescriptor {
	var ids []protoreflect.FieldDescriptor

	fields := md.Fields()
	for i := range fields.Len() {
		// OUTPUT_ONLY dominates: never preserved, even when it is also
		// IDENTIFIER, so its client value is cleared.
		if fd := fields.Get(i); serverManagedBehavior(fd, policy) == annotations.FieldBehavior_IDENTIFIER {
			ids = append(ids, fd)
		}
	}

	return ids
}

// preserveElementIdentifiers runs [fieldmask.FieldMask.preserveIdentifierFields] on
// every element of the repeated or map field fd (value v) that its sub-mask
// nested addresses with a sub-path, using the element mask the filter applies
// to it: for a list the "*" sub-mask; for a map the sub-mask of the entry's
// key, else the "*" one. Elements selected whole, or not at all, are skipped. The
// element mask is shared by the elements it addresses, so an identifier set
// on any of them is kept on all that carry one. Collections of scalars and of
// dynamic well-known types are skipped.
//
// The elements are not visited when no element mask can find an identifier
// (see [fieldmask.FieldMask.mayPreserve]), and a map without a "*" sub-mask is read by
// the keys the mask names instead of being scanned.
func preserveElementIdentifiers(fd protoreflect.FieldDescriptor, v protoreflect.Value, nested fieldmask.FieldMask, policy *options) {
	elem := wildcardElementDescriptor(fd, policy)
	if elem == nil {
		return
	}

	ids := identifierFields(elem, policy)

	wildcard, hasWildcard := nested[maskpath.Wildcard]
	if fd.IsList() {
		if wildcard == nil || !mayPreserve(wildcard, elem, ids, policy) {
			return
		}

		list := v.List()
		for i := range list.Len() {
			preserveIdentifiers(wildcard, list.Get(i).Message(), ids, policy)
		}

		return
	}

	mm := v.Map()

	if !hasWildcard {
		keyKind := fd.MapKey().Kind()

		for key, sub := range nested {
			if sub == nil || !mayPreserve(sub, elem, ids, policy) {
				continue
			}
			// A key that does not parse back to itself matches no entry,
			// as in the filter, which compares the entry's key string.
			k, ok := maskpath.ParseMapKey(keyKind, key)
			if !ok {
				continue
			}

			if val := mm.Get(k); val.IsValid() {
				preserveIdentifiers(sub, val.Message(), ids, policy)
			}
		}

		return
	}

	if !elementMasksMayPreserve(nested, elem, ids, policy) {
		return
	}

	mm.Range(func(k protoreflect.MapKey, val protoreflect.Value) bool {
		sub, ok := nested[k.String()]
		if !ok {
			sub = wildcard
		}

		if sub != nil {
			preserveIdentifiers(sub, val.Message(), ids, policy)
		}

		return true
	})
}

// mayPreserve reports whether [fieldmask.FieldMask.preserveIdentifiers] can add an
// entry anywhere in msk for a message of type md whose [identifierFields] are
// ids: md declares one, or a branch of msk reaches a singular message or
// collection whose element mask may. It follows the mask, not the schema, so
// it terminates on recursive types. For collections it over-approximates —
// every sub-mask is taken as an element mask — which only costs a visit that
// finds nothing.
func mayPreserve(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, ids []protoreflect.FieldDescriptor, policy *options) bool {
	if len(ids) > 0 {
		return true
	}

	fields := md.Fields()

	for name, nested := range msk {
		if nested == nil {
			continue
		}

		fd := fields.ByName(protoreflect.Name(name))
		switch {
		case fd == nil:
		case fd.IsList() || fd.IsMap():
			elem := wildcardElementDescriptor(fd, policy)
			if elem == nil {
				continue
			}

			if elementMasksMayPreserve(nested, elem, identifierFields(elem, policy), policy) {
				return true
			}
		case policy.expandable(fd):
			if mayPreserve(nested, fd.Message(), identifierFields(fd.Message(), policy), policy) {
				return true
			}
		}
	}

	return false
}

// elementMasksMayPreserve reports whether any non-nil sub-mask of nested — the
// sub-mask of a collection whose element type is elem with [identifierFields]
// ids — may preserve an identifier (see [fieldmask.FieldMask.mayPreserve]).
func elementMasksMayPreserve(nested fieldmask.FieldMask, elem protoreflect.MessageDescriptor, ids []protoreflect.FieldDescriptor, policy *options) bool {
	for _, sub := range nested {
		if sub != nil && mayPreserve(sub, elem, ids, policy) {
			return true
		}
	}

	return false
}

// hasProtectedDescendants reports whether [protectedDescendants] would report
// a violation for md: whether md holds a server-managed field directly or in a
// singular sub-message at any depth. visited breaks recursion on
// self-referential schemas.
func hasProtectedDescendants(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options) bool {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if serverManagedBehavior(fd, policy) != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return true
		}

		if policy.expandable(fd) && !visited[fd.Message().FullName()] && hasProtectedDescendants(fd.Message(), visited, policy) {
			return true
		}
	}

	return false
}

// serverManagedBehavior returns the server-managed behavior of fd that
// governs an update: OUTPUT_ONLY, then IDENTIFIER, then IMMUTABLE, whatever
// the declaration order, so OUTPUT_ONLY names a field that is also IDENTIFIER
// or IMMUTABLE. It returns FIELD_BEHAVIOR_UNSPECIFIED for any other field.
func serverManagedBehavior(fd protoreflect.FieldDescriptor, policy *options) annotations.FieldBehavior {
	return behavior.FirstMatch(policy.behaviors(fd), []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	})
}

// elementPath returns the path of the element sub-mask that [elementMasks]
// yields under key.
func elementPath(prefix, key string) string {
	return behavior.JoinPath(prefix, behavior.QuoteSegment(key))
}

// elementMasks yields the parts of nested, the sub-mask of the repeated or
// map field fd, that address fields of its element messages, each with the
// mask key leading to it. For a list: the "*" sub-mask, the only way a path
// addresses the elements of a repeated field (AIP-161: no index and no
// implicit per-element form). For a map: the sub-mask of every key, "*"
// included, in sorted order. A nil sub-mask names whole elements, which are
// replaced as a whole like the collection itself, and is not yielded.
func elementMasks(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask) iter.Seq2[string, fieldmask.FieldMask] {
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

// wildcardElementDescriptor returns the message descriptor used as the
// recursion target for the "*" wildcard on fd: the list element message
// for repeated fields or the map value message for maps. Returns nil
// for scalar collections and dynamic well-known types whose subtree is
// not navigable by the schema.
func wildcardElementDescriptor(fd protoreflect.FieldDescriptor, policy *options) protoreflect.MessageDescriptor {
	switch {
	case fd.IsList():
		if fd.Kind() != protoreflect.MessageKind {
			return nil
		}

		m := fd.Message()
		if m == nil || maskpath.IsDynamicMessage(m) || (policy.atomicMessage != nil && policy.atomicMessage(m)) {
			return nil
		}

		return m
	case fd.IsMap():
		v := fd.MapValue()
		if v == nil || v.Kind() != protoreflect.MessageKind {
			return nil
		}

		m := v.Message()
		if m == nil || maskpath.IsDynamicMessage(m) || (policy.atomicMessage != nil && policy.atomicMessage(m)) {
			return nil
		}

		return m
	}

	return nil
}

// firstUpdateMaskBehavior collapses the google.api.field_behavior annotation
// list of fd into the single value that governs update-mask semantics:
// REQUIRED, IMMUTABLE, OUTPUT_ONLY, or IDENTIFIER. INPUT_ONLY, UNORDERED_LIST
// and NON_EMPTY_DEFAULT have no update-mask meaning and are reported as
// FIELD_BEHAVIOR_UNSPECIFIED so the caller treats them as optional.
func firstUpdateMaskBehavior(fd protoreflect.FieldDescriptor, policy *options) annotations.FieldBehavior {
	for _, b := range policy.behaviors(fd) {
		switch b { //nolint:exhaustive // INPUT_ONLY, UNORDERED_LIST, NON_EMPTY_DEFAULT have no update-mask semantics.
		case annotations.FieldBehavior_REQUIRED,
			annotations.FieldBehavior_IMMUTABLE,
			annotations.FieldBehavior_OUTPUT_ONLY,
			annotations.FieldBehavior_IDENTIFIER:
			return b
		}
	}

	return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
}

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
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
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
// left empty, or an IMMUTABLE or IDENTIFIER value changed from stored state.
// Inspect Violations for per-field details. It maps to INVALID_ARGUMENT.
// Violations retain the base traversal order: explicit masks visit sorted mask
// branches, replacements visit declaration order, and covered paths follow the
// main selection. Newly diagnosed fields follow in declaration order, with list
// indexes ascending and map keys sorted. All entry points share this ordering.
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

// Apply applies an update mask to msg under AIP-134 and AIP-203, and writes
// back the effective mask to a non-nil msk. Equal IMMUTABLE and IDENTIFIER values
// are ignored with [WithCurrent]; changes, including clearing a selected value,
// are *[BehaviorError] violations naming the field. OUTPUT_ONLY paths are dropped
// and their input values cleared without an error. REQUIRED fields selected by
// the mask, or in a written replacement, must be non-empty.
//
// Without [WithCurrent], IMMUTABLE and IDENTIFIER values are retained for the
// caller to compare with the stored resource, and their paths are omitted from
// the effective mask. Use [Prepare] for two-stage updates: its plan retains
// explicit clear intent and completes through [Spec.Plan] or [Patch.Run].
// Use [ApplyMask] when the effective mask must be returned, especially
// for an omitted mask: Apply cannot assign a synthesized mask back to a nil map.
//
// A nil msk implies populated leaves; empty mutable messages contribute no
// path. A non-nil empty mask is a no-op that leaves msg untouched; "*" alone is a full replacement. "*"
// with other paths is invalid. Without stored state, a mask emptied by dropping
// paths is interim: completion may restore deferred writes, so always complete a
// [Prepare] plan. OUTPUT_ONLY input is cleared even when no paths remain. Never store the filtered request as the
// resource; merge only the effective paths into the stored resource.
//
// A singular message named whole is expanded when it contains protected fields,
// so its stored protected values survive. With current state, protected values
// covered by that replacement or by "*" must match, absence included. Whole
// collections and atomic values with protected descendants are deferred without
// current state: their covering paths are dropped, and their values are kept
// for comparison. [Prepare] retains that intent for completion. With current
// state, collection descendants are compared by index for lists and key for maps,
// including removed elements. Element sub-paths follow the same protection rules.
//
// The private filtering mask keeps routing identifiers and unverified protected
// input, without adding those paths to msk. Other fields outside the effective
// mask are cleared. A field selected but absent from msg remains absent, so a
// later merge clears it. [DropIdentifiers] is no longer needed after Apply.
//
// [WithIdentityFields] and [WithImmutableFields] govern top-level fields like
// the corresponding annotations; OUTPUT_ONLY dominates. A policy-only identity
// field not named in the mask is kept for routing or concurrency checks and is
// not compared. A populated annotated IDENTIFIER in a visited message must match
// current state even when unnamed. Oneof switches that could erase protected
// members remain guarded, conservatively without current state.
//
// Paths resolve to declared field names. Invalid paths follow [WithPathValidation];
// list indexes, atomic descent and strict dynamic-path violations are always
// rejected. Every path is checked before resolved covering paths collapse, but
// paths already collapsed by construction of msk cannot be recovered; use
// [ApplyMask] for raw requests. Limits apply to raw and expanded paths. Without
// current state, stored-dependent expansion and wildcard exclusions remain
// unresolved; [Prepare] retains them for completion and final limit checks.
//
// Path errors are *[fieldmask.ValidationError], behavior errors *[BehaviorError],
// and an invalid msg or mismatched current resource is [ErrMessageMismatch].
// A nil msg is a no-op. On error
// msk and msg are unchanged. Clone msk first if the original is needed later.
func Apply(msk fieldmask.FieldMask, msg proto.Message, opts ...Option) error {
	if msg == nil {
		return nil
	}

	work, err := cloneMessage(msg)
	if err != nil {
		return err
	}

	selection := msk.Clone()
	if err := apply(selection, work, opts...); err != nil {
		return err
	}

	if err := publishMessage(msg, work); err != nil {
		return err
	}

	if msk != nil {
		replaceMask(msk, selection)
	}

	return nil
}

func apply(msk fieldmask.FieldMask, msg proto.Message, opts ...Option) error {
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
		u.orderMode = referenceImplied
		msk = u.impliedSelection(root)
		u.checkImplied(msg.ProtoReflect().Descriptor(), root)
	case len(msk) == 0:
		return nil // a provided empty update_mask is a no-op (AIP-134).
	}

	descriptor := msg.ProtoReflect().Descriptor()

	all, err := msk.SelectsAll()
	if err != nil {
		return &fieldmask.ValidationError{Path: maskpath.Wildcard, Reason: maskpath.ReasonWildcardNotAlone}
	}

	var work fieldmask.FieldMask

	var covered []fieldmask.FieldMask

	if all {
		work = u.replaceAll(descriptor, root)
	} else {
		work, covered, err = resolveUpdatePaths(msk.ToPaths(), descriptor, policy, o)
		if err != nil {
			return err
		}

		u.checkCovering(work, covered, descriptor, root)
	}

	if err = u.maskRefusal(work, descriptor); err != nil {
		return err
	}

	violations, err := u.orderedViolations(descriptor)
	if err != nil {
		return err
	}

	if len(violations) > 0 {
		return &BehaviorError{Violations: violations}
	}

	replaceMask(msk, work)

	if len(work) == 0 {
		return clearOutputOnly(msg, policy) // no writes, but output-only input is still cleared
	}

	keep := work.Clone()
	preserveIdentifierFields(keep, msg, policy)

	if !u.verify {
		preserveUnverified(keep, msg.ProtoReflect(), policy)
	}

	maskwalk.Filter(keep, msg, false)

	return clearOutputOnlyKeeping(msg, keep, policy)
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
		name, rejected := maskpath.ResolvePath(md, path, policy.strictDynamicPaths)
		if rejected != nil {
			rejected = validationError(rejected)
			name = path // kept as written: it matches no field
		}

		if rejected != nil && o.shouldValidatePaths() {
			if o.pathValidation == PathValidationEnforce {
				return nil, nil, rejected // fail-fast on the first schema-invalid path.
			}
			// PathValidationWarn: report every bad path but still apply the
			// mask. shouldValidatePaths guarantees a non-nil reporter here.
			if validationErr, ok := errors.AsType[*fieldmask.ValidationError](rejected); ok {
				o.pathReporter(validationErr)
			}
		}

		if err := maskpath.RejectStructural(md, name, policy.strictDynamicPaths); err != nil {
			return nil, nil, validationError(err)
		}

		if err := policy.rejectAtomicDescent(md, name); err != nil {
			return nil, nil, err // a path into an atomic value is refused in every mode
		}

		if rejected != nil {
			// A path the schema rejects that o lets through matches no field:
			// it is left out of the mask, so it neither covers another path
			// ("owner.*" on a singular message beside "owner.email") nor
			// names a map key the checks, the filter and a later merge would
			// keep out of a "*" ("delegates.k.typo" beside "delegates.*").
			continue
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

	return work, coveredMasks(work, paths)
}

// coveredMasks returns the masks of the paths that work, the mask built from
// them, covers ([isCovered]), one per distinct path, in one pass: a path
// repeated in the mask is checked once. The complete merge effect is evaluated
// once per call; covered input checks share the call's traversal budget.
func coveredMasks(work fieldmask.FieldMask, paths [][]string) []fieldmask.FieldMask {
	var (
		covered []fieldmask.FieldMask
		seen    map[string]bool
	)

	for _, parts := range paths {
		if !isCovered(work, parts) {
			continue
		}

		key := maskpath.Join(parts)
		if seen[key] {
			continue
		}

		if seen == nil {
			seen = map[string]bool{}
		}

		seen[key] = true
		single := fieldmask.FieldMask{}
		maskwalk.Insert(single, parts)
		covered = append(covered, single)
	}

	return covered
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
			wildcard, whole := level[maskpath.Wildcard]
			return whole && wildcard == nil // removed beside a whole-element "*"
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
// masks of the paths work covers ([coverPaths]). The protected-change verdict
// comes from the complete selection once: covered paths cannot add writes
// to its effect. Their input contracts (including element counts and REQUIRED
// fields) are still checked individually without cloning either resource.
// A violation both report is recorded once.
func (u *updateCheck) checkCovering(work fieldmask.FieldMask, covered []fieldmask.FieldMask, md protoreflect.MessageDescriptor, root msgPair) {
	if u.orderMode == referenceExplicit && u.violationOrder == nil {
		u.violationOrder = append(u.violationOrder, work.Clone())
		for _, tree := range covered {
			u.violationOrder = append(u.violationOrder, tree.Clone())
		}
	}

	u.evaluateSelection(work, root)

	if u.refusal != nil {
		return
	}

	u.checkMask(work, md, []msgPair{root}, "")

	if len(covered) == 0 {
		return
	}

	for _, m := range covered {
		u.inputSelection(m, md)

		if u.refusal != nil {
			return
		}

		if u.verify {
			if err := maskmerge.CheckElementCounts(root.cur, root.req, m, md, ""); err != nil {
				u.refuse(err)
				return
			}
		} else {
			u.needsCurrent = u.needsCurrent || selectionNeedsStored(m, md)
		}

		u.checkMask(m, md, []msgPair{root}, "")
	}
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
	// the others compared or deferred.
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
		sub, ok := maskwalk.KeyMask(nested, k.String())
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

		if (fd.IsList() || fd.IsMap()) && maskpath.ValueMessage(fd) != nil &&
			holdsImmutable(maskpath.ValueMessage(fd), map[protoreflect.FullName]bool{}, policy) {
			return true
		}

		if policy.expandable(fd) && !visited[fd.Message().FullName()] && hasProtectedDescendants(fd.Message(), visited, policy) {
			return true
		}

		// An atomic value holding a protected field is compared, not
		// expanded: the message holding it is expanded to reach it.
		if isProtectedAtomic(fd, policy) {
			return true
		}
	}

	return false
}

// isAtomicValue reports whether fd is a singular message field of a type the
// policy keeps whole ([WithAtomicMessage]); the dynamic Struct, Value and
// ListValue never are.
func isAtomicValue(fd protoreflect.FieldDescriptor, policy *options) bool {
	md := fd.Message()

	return !fd.IsList() && !fd.IsMap() && md != nil && policy.atomicMessage != nil && policy.atomicMessage(md) && !maskpath.IsDynamicMessage(md)
}

// isProtectedAtomic reports whether fd holds a protected leaf: a singular
// value of an atomic type that holds a field a whole replacement must keep
// ([atomicProtectedPath]). Such a value is one protected leaf everywhere — it
// is compared whole, guards its oneof, keeps its parent expanded and is named
// on its own in an implied mask.
func isProtectedAtomic(fd protoreflect.FieldDescriptor, policy *options) bool {
	return isAtomicValue(fd, policy) && isProtectedAtomicType(fd.Message(), policy)
}

// isProtectedAtomicType reports whether md is an atomic type holding a field a
// whole replacement of it must keep ([atomicProtectedPath]).
func isProtectedAtomicType(md protoreflect.MessageDescriptor, policy *options) bool {
	if md == nil || policy.atomicMessage == nil || !policy.atomicMessage(md) || maskpath.IsDynamicMessage(md) {
		return false
	}

	path, _ := atomicProtectedPath(md, map[protoreflect.FullName]bool{}, policy)

	return path != ""
}

// atomicProtectedPath returns the path below a value of md, written whole,
// of the first field that value must keep, with its behavior, or "" when there
// is none: an IMMUTABLE or IDENTIFIER field of md or any nested message,
// including collection elements. OUTPUT_ONLY fields are not descended.
// visited breaks recursion on self-referential schemas.
func atomicProtectedPath(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options) (string, annotations.FieldBehavior) {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		switch b := serverManagedBehavior(fd, policy); b { //nolint:exhaustive // serverManagedBehavior returns only these or UNSPECIFIED.
		case annotations.FieldBehavior_OUTPUT_ONLY:
			continue
		case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
			return string(fd.Name()), b
		}

		child := maskpath.ValueMessage(fd)
		if child == nil || maskpath.IsDynamicMessage(child) || visited[child.FullName()] {
			continue
		}

		if sub, b := atomicProtectedPath(child, visited, policy); sub != "" {
			return behavior.JoinPath(string(fd.Name()), sub), b
		}
	}

	return "", annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
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

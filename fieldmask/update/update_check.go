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
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// Behavior sets skipped by the REQUIRED walks of an update. Singular
// protected fields are checked separately against stored state and excluded
// from the write mask. Collection elements are replaced whole, so their
// REQUIRED fields are checked as written, as well as their protected values.
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
// would change. Unverified values are deferred outside the effective mask.
type updateCheck struct {
	policy   *options
	deferred *DeferredPlan
	// verify reports that the stored resource is known: protected values are
	// compared with it.
	verify bool
	// needsCurrent records any stored-dependent validation or normalization,
	// independently of whether it emits a protected-field operation.
	needsCurrent bool
	// top, when set, overrides the behavior of top-level fields.
	top topBehavior
	// addressOnly, when set, reports the top-level fields that address the
	// resource without being compared unless the mask names them: the
	// identity fields of the policy ([WithIdentityFields]) that carry no
	// IDENTIFIER annotation.
	addressOnly func(fd protoreflect.FieldDescriptor) bool
	// preparedInput retains the selection before protected write paths are pruned.
	preparedInput fieldmask.FieldMask
	// namedInput preserves explicit naming for routing checks after ignored paths
	// are removed; it is never used to evaluate a merge or derive writes.
	namedInput fieldmask.FieldMask
	// resolvedWhole retains REQUIRED checks on whole messages expanded to leaves.
	resolvedWhole  map[string]bool
	resolvedOneofs map[string]bool
	resolved       bool
	violations     []FieldViolation
	violationSet   map[FieldViolation]struct{}
	deferredSet    map[DeferredOperation]struct{}
	// Retain the original selection and read-only resource pair for the reference
	// walk. Only an evaluation with violations runs that walk.
	violationOrder []fieldmask.FieldMask
	orderRoot      msgPair
	orderMode      referenceOrderMode
	orderSkip      []string
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
}

// newUpdateCheck returns the check of an update of req whose stored value is
// current (nil when unknown), and the root pair. A current message that is
// invalid or of another type than req is [ErrMessageMismatch].
func newUpdateCheck(req, current proto.Message, policy *options) (*updateCheck, msgPair, error) {
	if !validMessage(req) {
		return nil, msgPair{}, ErrMessageMismatch
	}

	u := &updateCheck{policy: policy}
	root := msgPair{req: req.ProtoReflect()}

	if current != nil {
		if _, err := reflectPair(req, current); err != nil {
			return nil, msgPair{}, err
		}

		snapshot, err := cloneMessage(current)
		if err != nil {
			return nil, msgPair{}, err
		}

		u.verify = true
		root.cur = snapshot.ProtoReflect()
	}

	u.orderRoot = root

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
		return policy.classifyField(fd, true).managedBehavior(), false
	}
}

// isPolicyIdentity reports whether the top-level field fd is an identity
// field of the policy only: named by [WithIdentityFields], neither annotated
// IDENTIFIER nor OUTPUT_ONLY. Such a field (id, etag by default) addresses
// the resource; the update keeps it but compares it with the stored value
// only when the mask names it, so a stale etag is left to the service's
// concurrency check (AIP-154).
func isPolicyIdentity(fd protoreflect.FieldDescriptor, policy *options) bool {
	if !slices.Contains(policy.identityFields, string(fd.Name())) {
		return false
	}

	f := policy.classifyField(fd, false)

	return !f.outputOnly && !f.identifier
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

		for _, violation := range v {
			u.fail(violation)
		}
	}
}

// fail records a violation.
func (u *updateCheck) fail(v FieldViolation) {
	if _, exists := u.violationSet[v]; exists {
		return
	}

	if u.violationSet == nil {
		u.violationSet = make(map[FieldViolation]struct{})
	}

	u.violationSet[v] = struct{}{}
	u.violations = append(u.violations, v)
}

// checkMask applies the update rules to msk, the sub-mask of message type md
// whose addressed messages are pairs, under the mask path prefix. It mutates
// msk: dropped entries are deleted, a branch left empty is deleted with them,
// and a replaced message holding server-managed fields becomes the sub-mask
// of its other fields.
func (u *updateCheck) checkMask(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []msgPair, prefix string) {
	if maskpath.IsDynamicMessage(md) {
		return
	}

	u.verifyUnnamedIdentifiers(msk, md, pairs, prefix)

	fields := md.Fields()
	for _, name := range checkedFieldNames(msk, fields, u.resolved) {
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
			u.omitProtected(fd, nested, pairs, path, b)
			delete(msk, name)

			continue
		}

		fieldPairs := pairs

		if u.resolved {
			fieldPairs = u.resolvedFieldPairs(fd, nested, pairs, path)
			if len(fieldPairs) == 0 {
				continue
			}
		}

		if nested == nil {
			u.checkWhole(msk, name, fd, fieldPairs, path)
			continue
		}

		switch {
		case fd.IsList() || fd.IsMap():
			u.checkElements(fd, nested, fieldPairs, path)
		case u.policy.expandable(fd):
			u.checkMask(nested, fd.Message(), childPairs(fieldPairs, fd), path)
		default:
			continue
		}

		if len(nested) == 0 {
			delete(msk, name)
		}
	}
}

func checkedFieldNames(msk fieldmask.FieldMask, fields protoreflect.FieldDescriptors, resolved bool) []string {
	if !resolved {
		return slices.Sorted(maps.Keys(msk))
	}

	names := make([]string, 0, len(msk))

	for i := range fields.Len() {
		name := string(fields.Get(i).Name())
		if _, selected := msk[name]; selected {
			names = append(names, name)
		}
	}

	return names
}

// omitProtected excludes a protected selection from the effective mask. With
// stored state, evaluateSelection has already checked the complete merge effect;
// this step makes no equality decision. Without it, retain a deferred comparison.
func (u *updateCheck) omitProtected(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string, b annotations.FieldBehavior,
) {
	if !u.verify {
		u.deferProtected(fd, nested, pairs, path, b)
	}
}

// verifyUnnamedIdentifiers compares the populated IDENTIFIER fields of md
// that msk does not name with the stored ones: the update keeps them to
// address the resource, so they must not differ from it. Without a stored
// resource they are accepted as the address. This is input consistency, not a
// merge-effect decision: an unnamed routing value is never written or pruned by
// this check. The original selection, including all implicit clears, is checked
// separately against the complete resource before this walk.
func (u *updateCheck) verifyUnnamedIdentifiers(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, pairs []msgPair, prefix string) {
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if _, named := msk[string(fd.Name())]; named || u.namedInput.Contains(behavior.JoinPath(prefix, string(fd.Name()))) {
			continue
		}

		if b, skip := u.behaviorOf(fd, prefix == ""); skip || b != annotations.FieldBehavior_IDENTIFIER || u.isAddressOnly(fd, prefix == "") {
			continue
		}

		for _, p := range pairs {
			if p.req != nil && p.req.Has(fd) && !u.verify {
				u.deferProtected(fd, nil, []msgPair{p}, behavior.JoinPath(prefix, string(fd.Name())), annotations.FieldBehavior_IDENTIFIER)
				continue
			}

			if p.req != nil && p.req.Has(fd) && !behavior.SameInputValue(p.req, p.cur, fd, u.policy.behaviors) {
				u.fail(behavior.ProtectedViolation(behavior.JoinPath(p.path, string(fd.Name())), annotations.FieldBehavior_IDENTIFIER))
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
	if u.resolved && len(pairs) > 1 && u.policy.classifyField(fd, false).required {
		pairs = u.resolvedRequiredPairs(fd, pairs)
		if len(pairs) == 0 {
			return
		}
	} else if u.checkRequired(fd, pairs) {
		return
	}

	u.checkWrittenRequired(fd, pairs)

	if !u.checkReplacement(fd, path) {
		delete(msk, name)
		return
	}

	if !u.policy.expandable(fd) || !hasProtectedDescendants(fd.Message(), map[protoreflect.FullName]bool{}, u.policy) {
		return
	}

	if sub := u.expand(fd.Message(), childPairs(pairs, fd), path); len(sub) > 0 {
		msk[name] = sub
	} else {
		delete(msk, name)
	}
}

// checkRequired records unsatisfied REQUIRED fields and reports whether any failed.
func (u *updateCheck) checkRequired(fd protoreflect.FieldDescriptor, pairs []msgPair) bool {
	if !u.policy.classifyField(fd, false).required {
		return false
	}

	failed := false

	for _, p := range pairs {
		if behavior.RequiredUnmet(p.req, fd) {
			path := behavior.JoinPath(p.path, string(fd.Name()))
			u.fail(FieldViolation{Path: path, Behavior: annotations.FieldBehavior_REQUIRED, Reason: behavior.ReasonRequired})

			failed = true
		}
	}

	return failed
}

// isAtomic reports whether md is a message type the policy keeps whole
// ([WithAtomicMessage]); the dynamic Struct, Value and ListValue never are.
func (u *updateCheck) isAtomic(md protoreflect.MessageDescriptor) bool {
	return md != nil && u.policy.atomicMessage != nil && u.policy.atomicMessage(md) && !maskpath.IsDynamicMessage(md)
}

// checkWrittenRequired records the REQUIRED fields left empty in the value of
// fd each pair's request writes: the populated subtree of a singular message
// (its IMMUTABLE fields excepted, which keep their stored value when absent)
// and every element of a repeated or map field of messages.
func (u *updateCheck) checkWrittenRequired(fd protoreflect.FieldDescriptor, pairs []msgPair) {
	for _, p := range pairs {
		if p.req == nil || !p.req.Has(fd) {
			continue
		}

		path := behavior.JoinPath(p.path, string(fd.Name()))
		checkWrittenRequired(p.req, fd, path, u.policy.behaviors, u.isAtomic, u.required)
	}
}

// checkWrittenRequired shares the unchanged REQUIRED traversal with the base
// diagnostic walk. Each caller keeps its own recorder and atomic policy.
func checkWrittenRequired(req protoreflect.Message, fd protoreflect.FieldDescriptor, path string, resolve behavior.Resolve,
	atomic func(protoreflect.MessageDescriptor) bool, record func(string) func([]FieldViolation, bool),
) {
	switch {
	case fd.IsList():
		if fd.Kind() != protoreflect.MessageKind {
			return
		}

		list := req.Get(fd).List()
		for i := range list.Len() {
			elemPath := behavior.JoinPath(path, strconv.Itoa(i))
			record(elemPath)(behavior.RequiredViolations(list.Get(i).Message(), elemPath, resolve, elementRequiredSkip, requiredMaxDepth))
		}
	case fd.IsMap():
		if fd.MapValue().Kind() != protoreflect.MessageKind {
			return
		}

		mm := req.Get(fd).Map()
		for _, k := range behavior.SortedMapKeys(mm) {
			elemPath := behavior.JoinPath(path, behavior.QuoteSegment(k.String()))
			record(elemPath)(behavior.RequiredViolations(mm.Get(k).Message(), elemPath, resolve, elementRequiredSkip, requiredMaxDepth))
		}
	case fd.Message() != nil:
		skip := updateRequiredSkip
		if atomic(fd.Message()) {
			skip = elementRequiredSkip // an atomic value is written as sent, IMMUTABLE fields included
		}

		record(path)(behavior.ReplacedRequiredViolations(req.Get(fd).Message(), path, resolve, skip, elementRequiredSkip,
			requiredMaxDepth, nil, atomic))
	}
}

// checkImplied compares all supplied protected values, including identifiers
// and branches omitted from the synthesized write mask. Its expansion is only
// for validation; the implied mask still selects just the populated fields.
func (u *updateCheck) checkImplied(md protoreflect.MessageDescriptor, root msgPair) {
	if maskpath.IsDynamicMessage(md) {
		return
	}

	u.validationOnly = true
	u.expand(md, []msgPair{root}, "")
	u.validationOnly = false
}

// expand returns the sub-mask that replaces a singular message of type md
// (whose request and stored values are pairs, at the mask path prefix) while
// keeping its server-managed values: every field but the OUTPUT_ONLY ones,
// and a sub-message holding a server-managed field replaced the same way.
// IMMUTABLE and IDENTIFIER fields are compared, absence included, with current
// state. Without it their paths are omitted and comparison is the caller's duty.
//
// A sub-message neither the request nor the stored resource holds is left
// out: there is nothing to replace. A member of a real oneof is written only
// when a request sets it or, to clear it, the stored resource holds it, so a
// replacement switches no oneof by itself. Without stored state, absent branches
// are unresolved: Prepare retains the covering intent for completion instead of
// emitting speculative paths that might exceed the final mask limits.
func (u *updateCheck) expand(md protoreflect.MessageDescriptor, pairs []msgPair, prefix string) fieldmask.FieldMask {
	if !u.expandWithin(prefix) {
		return nil
	}

	if !u.verify && !u.validationOnly {
		u.needsCurrent = true
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
			if u.expandLeaf(fd, pairs, path) {
				out[name] = nil
			}

			if u.refusal != nil {
				return nil
			}

			continue
		}

		reqHas, curHas := pairsHold(pairs, fd)

		switch {
		case u.validationOnly && !reqHas:
			continue // only the protected values the request sets are compared
		case !reqHas && !curHas:
			continue
		}

		sub := u.expand(fd.Message(), childPairs(pairs, fd), path)

		if len(sub) > 0 {
			out[name] = sub
		}
	}

	return out
}

// expandLeaf checks a whole field of a covering replacement and reports
// whether its path belongs in the expansion. An implied comparison-only walk
// visits only request values; deferred replacements emit no path.
func (u *updateCheck) expandLeaf(fd protoreflect.FieldDescriptor, pairs []msgPair, path string) bool {
	if reqHas, _ := pairsHold(pairs, fd); u.validationOnly && !reqHas {
		return false
	}

	if !u.checkReplacement(fd, path) {
		return false
	}

	return u.emitLeaf(path)
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
// and is held to neither. Stateless expansions are interim masks: their final
// limits are checked with stored state; the traversal budget still applies.
func (u *updateCheck) emitLeaf(path string) bool {
	if u.validationOnly || !u.verify {
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

// checkSetProtected records deferred fields covered by a replacement. Stateful
// replacement effects have already been checked on the complete resource. The
// validation-only implied walk additionally verifies supplied protected input
// excluded by synthesis (identifiers and configured skips), selecting each such
// value whole. Whole-value equality is exact for that input contract; this check
// neither prunes the mask nor substitutes for original-selection evaluation.
func (u *updateCheck) checkSetProtected(fd protoreflect.FieldDescriptor, pairs []msgPair, path string, b annotations.FieldBehavior) {
	if reqHas, _ := pairsHold(pairs, fd); u.validationOnly && !reqHas {
		return
	}

	if !u.verify {
		u.deferProtected(fd, nil, pairs, path, b)
		return
	}

	if !u.validationOnly {
		return // the complete original selection was evaluated before expansion
	}

	for _, p := range pairs {
		if !behavior.SameInputValue(p.req, p.cur, fd, u.policy.behaviors) {
			u.fail(behavior.ProtectedViolation(behavior.JoinPath(p.path, string(fd.Name())), b))
		}
	}
}

// replaceAll returns the mask of a full replacement ("*") of the root pair's
// message type md: [updateCheck.expand] of the root, after the REQUIRED
// fields of the request are checked as a replacement writes them. The
// replacement does not write a top-level field the update skips or governs
// as OUTPUT_ONLY, IDENTIFIER or IMMUTABLE — by annotation or by the policy
// ([WithImmutableFields], [WithIdentityFields]) — nor anything below it: an
// selected value must match the stored one, so its REQUIRED
// fields, and those below it, are not checked.
func (u *updateCheck) replaceAll(md protoreflect.MessageDescriptor, root msgPair) fieldmask.FieldMask {
	u.orderMode = referenceReplacement
	selection := u.replacementSelection(md)
	u.evaluateSelection(selection, root)

	if maskpath.IsDynamicMessage(md) {
		return selection
	}

	if u.refusal != nil {
		return nil
	}

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

	return u.expand(md, []msgPair{root}, "")
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

	// Stored elements can be selected even when the request has no list.
	// Wildcard exclusions also depend on which keyed operations survive.
	if !u.verify {
		u.needsCurrent = true
	}

	// named is the selection as sent; nested is cleaned below.
	named := maps.Clone(nested)

	// A whole element replacement checks REQUIRED fields as written and
	// protected descendants against stored state, or defers without it.
	for _, key := range slices.Sorted(maps.Keys(named)) {
		if named[key] == nil && (fd.IsMap() || key == maskpath.Wildcard) {
			u.checkWholeElements(fd, key, named, pairs)

			if !u.checkElementReplacement(fd, elementPath(path, key)) {
				delete(nested, key)
			}
		}
	}

	elem := wildcardElementDescriptor(fd, u.policy)
	if elem == nil {
		return
	}

	var emptied []string

	for key, sub := range elementMasks(fd, named) {
		selected := elementPairs(fd, key, named, pairs)
		if u.resolved && fd.IsMap() {
			// The resolved scope can include a key even when this request
			// carries no entry whose values can be validated.
			selected = slices.DeleteFunc(selected, func(pair msgPair) bool { return pair.req == nil })
		}

		u.checkMask(sub, elem, selected, elementPath(path, key))

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

	if !u.verify {
		// A removed key may return after completion. Keep its exclusion and
		// this wildcard together in the captured raw mask; a temporary pin
		// can exceed limits or be impossible to represent at all.
		u.needsCurrent = true

		delete(nested, maskpath.Wildcard)

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
		if count, ok := u.refusal.(*maskmerge.ElementCountError); ok {
			return &elementCountError{Path: count.Path, Reason: count.Reason}
		}

		return u.refusal
	}

	err := u.policy.validateMaskLimits(declaredPaths(work, md))
	if err != nil && !u.verify && u.needsCurrent {
		// This is an interim mask. Retain the operation in the original
		// snapshot, return no unsafe partial writes, and check final limits
		// through the stateful path during completion. Raw limits and the
		// traversal budget have already been enforced independently.
		clear(work)
		return nil
	}

	return err
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
				for i := range maskmerge.ListLen(stored) {
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
		k, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), maskpath.JSONKey(key))
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

// keepInputSelection retains the original keyed exclusions when ignoring output.
func keepInputSelection(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, policy *options) bool {
	return maskmerge.KeepInputSelection(fd, nested, policy.behaviors)
}

// clearOutputOnly clears every OUTPUT_ONLY value of msg at any depth,
// collection elements included: AIP-203 requires a service to clear them from
// a request. It runs after the request was filtered, so it only clears: the
// AIP-202 format checks of [fieldbehavior.Strip] are off (an update mask
// check reports no format violation). Representation conversion can still fail;
// callers must keep the request private until sanitization succeeds.
func clearOutputOnly(msg proto.Message, policy *options) error {
	return fieldbehavior.Strip(msg,
		fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY),
		fieldbehavior.WithResolver(policy.resolver),
		fieldbehavior.WithFieldFormats(false),
		fieldbehavior.WithMaxDepth(math.MaxInt))
}

// clearOutputOnlyKeeping sanitizes the request while preserving protected
// descendants of whole replacements. With current state, these values come
// from stored, including ignored OUTPUT_ONLY descendants. Stateless normalization
// only clears ignored input; it must never restore client OUTPUT_ONLY values.
func clearOutputOnlyKeeping(msg proto.Message, msk fieldmask.FieldMask, policy *options) error {
	var (
		kept    []keptValue
		current protoreflect.Message
	)

	if policy.current != nil {
		snapshot, err := cloneMessage(policy.current)
		if err != nil {
			return err
		}

		current = snapshot.ProtoReflect()
	}

	if err := clearOutputOnly(msg, policy); err != nil {
		return err
	}

	// Strip publishes replacement children. Resolve restoration parents only
	// after it finishes, so every target belongs to the request's current tree.
	if current != nil {
		if err := collectKept(msk, msg.ProtoReflect(), current, policy, &kept); err != nil {
			return err
		}
	}

	for _, k := range kept {
		if err := maskmerge.SetValue(k.parent, k.fd, k.value); err != nil {
			return conversionError(err)
		}
	}

	return nil
}

// keptValue is a protected field to restore after clearing OUTPUT_ONLY input.
type keptValue struct {
	parent protoreflect.Message
	fd     protoreflect.FieldDescriptor
	value  protoreflect.Value
}

// collectKept follows the actual merge targets, including whole collections
// and elements named by a key or wildcard.
func collectKept(msk fieldmask.FieldMask, m, current protoreflect.Message, policy *options, kept *[]keptValue) error {
	if m == nil || !m.IsValid() || maskpath.IsDynamicMessage(m.Descriptor()) {
		return nil
	}

	fields := m.Descriptor().Fields()

	for name, nested := range msk {
		fd := fields.ByName(protoreflect.Name(name))
		if fd == nil || !m.Has(fd) || maskpath.ValueMessage(fd) == nil {
			continue
		}

		if fd.IsList() || fd.IsMap() {
			if err := collectKeptElements(fd, nested, []msgPair{{req: m, cur: current}}, policy, kept); err != nil {
				return err
			}

			continue
		}

		child, stored := behavior.Child(m, fd), behavior.Child(current, fd)
		if nested == nil {
			if err := snapshotProtected(child, stored, policy, kept); err != nil {
				return err
			}
		} else if err := collectKept(nested, child, stored, policy, kept); err != nil {
			return err
		}
	}

	return nil
}

// collectKeptElements visits exactly the elements a collection mask writes.
func collectKeptElements(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, policy *options, kept *[]keptValue) error {
	if nested == nil {
		for _, pair := range replacementPairs(fd, pairs) {
			if err := snapshotProtected(pair.req, pair.cur, policy, kept); err != nil {
				return err
			}
		}

		return nil
	}

	for key, sub := range nested {
		for _, pair := range elementPairs(fd, key, nested, pairs) {
			if sub == nil {
				if err := snapshotProtected(pair.req, pair.cur, policy, kept); err != nil {
					return err
				}
			} else if err := collectKept(sub, pair.req, pair.cur, policy, kept); err != nil {
				return err
			}
		}
	}

	return nil
}

// snapshotProtected retains validated values through singular messages and
// collection elements, stopping at each protected value copied whole.
func snapshotProtected(m, current protoreflect.Message, policy *options, kept *[]keptValue) error {
	if m == nil || !m.IsValid() || maskpath.IsDynamicMessage(m.Descriptor()) {
		return nil
	}

	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}

		switch serverManagedBehavior(fd, policy) { //nolint:exhaustive // Only these or UNSPECIFIED.
		case annotations.FieldBehavior_OUTPUT_ONLY:
			continue
		case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
			if current == nil || !current.Has(fd) {
				continue
			}

			holder := m.Type().New()
			if err := maskmerge.SetValue(holder, fd, current.Get(fd)); err != nil {
				return conversionError(err)
			}

			*kept = append(*kept, keptValue{parent: m, fd: fd, value: holder.Get(fd)})

			continue
		}

		md := maskpath.ValueMessage(fd)
		if md == nil || maskpath.IsDynamicMessage(md) || !holdsImmutable(md, map[protoreflect.FullName]bool{}, policy) {
			continue
		}

		if fd.IsList() || fd.IsMap() {
			for _, pair := range replacementPairs(fd, []msgPair{{req: m, cur: current}}) {
				if err := snapshotProtected(pair.req, pair.cur, policy, kept); err != nil {
					return err
				}
			}
		} else if err := snapshotProtected(behavior.Child(m, fd), behavior.Child(current, fd), policy, kept); err != nil {
			return err
		}
	}

	return nil
}

// replaceMask makes msk hold exactly the entries of src.
func replaceMask(msk, src fieldmask.FieldMask) {
	clear(msk)
	maps.Copy(msk, src)
}

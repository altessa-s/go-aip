// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// Plan is the resolved update mask of one request.
type Plan struct {
	// Paths are the declared mutable paths merged from the request, as
	// [MergePaths] takes them: a field path replaces the field, a nested path
	// one leaf, a map key path one entry, and a sub-path through a map key or
	// "*" the fields of the elements it addresses. May be empty before stored-state
	// validation and deferred replacements have been completed.
	Paths []string
	// set lists the secrets the request populates; named lists the secrets
	// the mask names explicitly.
	set, named []string
	// secrets holds Patch's validated input until the request can be published.
	secrets map[string]string
}

// Empty reports that the plan contains no writes or secret operations. Without
// stored-state validation it is interim and does not permit skipping completion
// of a [Prepare] plan. Only a successfully completed empty plan is a no-op.
func (p Plan) Empty() bool {
	return len(p.Paths) == 0 && len(p.set) == 0 && len(p.named) == 0
}

// Secret returns the service input of the write-only field name: value when
// the request sets it, an empty string (clear) when the mask names it unset,
// and nil (keep the stored value) otherwise. Plans passed to [Patch.Save] retain
// selected string values privately, since Run publishes the request only after
// Save succeeds; for those fields the retained value takes precedence over value.
func (p Plan) Secret(name string, value *string) *string {
	switch {
	case slices.Contains(p.set, name):
		if captured, ok := p.secrets[name]; ok {
			return new(captured)
		}

		return value
	case slices.Contains(p.named, name):
		return new("")
	default:
		return nil
	}
}

// Plan resolves the update mask of resource (AIP-134) under the field
// behaviors of AIP-203. Pass the stored resource with [WithCurrent]: only
// then can an unchanged IMMUTABLE or IDENTIFIER value be told from a change.
// The update policy is the spec's own, so the policy options and
// [WithPathValidation] do not apply. The modes follow the presence of mask:
//   - a nil mask (update_mask omitted) is the implied mask of the mutable
//     leaf fields resource populates ([ImpliedPaths]; identity, secret,
//     immutable and server-managed fields left out at every depth);
//   - a mask with no paths (update_mask sent empty) is a no-op: the plan is
//     [Plan.Empty], nothing is checked or written and no secret
//     changes, whatever resource holds;
//   - "*" alone is a full replacement: every mutable field, a mutable message
//     holding a server-managed field (OUTPUT_ONLY, IDENTIFIER or IMMUTABLE)
//     at any depth expanded to its other fields so the replacement keeps the
//     stored server-managed values;
//   - otherwise each path, in any spelling of its field segments, must be an
//     update path ([ResolvePath]) or a top-level secret; unknown paths,
//     paths below a secret and the identity fields named on construction (id,
//     etag) without an IDENTIFIER annotation are refused. A path naming a
//     message that holds a server-managed field is expanded as "*" expands
//     one. Every path is resolved and checked on its own before the mask
//     collapses: a path another path covers ("profile.id" with "profile") is
//     checked as if sent alone — field behaviors, element counts and oneof
//     switches — and refused on its own index.
//
// An update path walks singular messages, map keys and the "*" wildcard of
// a repeated or map field (AIP-161), the grammar [Apply]
// accepts. Naming a collection whole ("labels", "aliases"), or with a
// trailing "*" ("labels.*", "aliases.*"), replaces it whole. A map key
// ("labels.k", "labels.`a.b`", "ports.443" for an integer key, spelled as
// the key kind formats it) replaces that entry, or deletes it when resource
// does not hold it. A sub-path through a key or "*" ("labels.k.display_name",
// "aliases.*.display_name") updates the elements it addresses in place: for
// a map the entry of that key, or with "*" every entry resource holds that no
// other path names by key; for a list every element, paired by index with the
// stored element, so resource must hold as many elements as the stored
// resource ([ErrElementCountMismatch]) or none (the sub-path is then cleared
// in every stored element). A list index ("aliases.0") and a field of the
// element type spelled without "*" ("aliases.display_name") are refused.
// A google.protobuf.Struct is a map<string, Value>: "attrs" or "attrs.*"
// replaces it whole and a key ("attrs.color", "attrs.`a.b`") replaces that
// entry or deletes it. By default a path goes on into the dynamic JSON, an
// extension beyond AIP-161 ([MergePaths] has the semantics):
// "attrs.work.from" replaces or deletes a nested key, "attrs.*.from" does so
// in every entry resource holds, "attrs.items.*.name" and "list_data.*.name"
// update list elements in place by index (the count rule of a repeated
// field applies), and "value_data.key" addresses a key of a
// google.protobuf.Value field. The dynamic JSON has no field behaviors. With
// [WithStrictDynamicPaths] on the spec such deeper paths are refused.
//
// The behaviors apply at any depth, collection elements included, in every
// mode:
//   - OUTPUT_ONLY: a path naming or crossing one is dropped, never an error.
//   - IMMUTABLE (annotated, or named with [WithImmutableFields]) and
//     IDENTIFIER: a path naming one, and in the implied and "*" modes or a
//     replaced message every one the resource sets, is compared with
//     [WithCurrent]: an equal value is dropped, a changed one is refused
//     ([ErrProtectedField]). Without [WithCurrent] the value is retained and
//     its path omitted. A [Prepare] plan supplied with [WithDeferredPlan] or
//     [WithPlanContext] retains that comparison for WithCurrent or [Patch]. An omitted value in an implied
//     mask keeps its stored value; a replacement compares absence too. An element updated in place through a
//     sub-path is compared with its stored element the same way.
//   - REQUIRED: a path naming a REQUIRED field the resource leaves absent or
//     falsy is refused ([ErrBehaviorViolation]), and so is a REQUIRED field
//     left empty in a value the update writes whole: a replaced message
//     (recursing only into its populated sub-messages), every element of a
//     replaced collection or map entry, and resource itself under "*".
//
// A collection or atomic value with protected descendants is deferred without
// current state: its whole path is omitted, since a replacement cannot exclude
// those descendants. With current state protected descendants are compared,
// including removed elements, by list index or map key. OUTPUT_ONLY input must
// be cleared before merging ([Patch] does; direct callers can use
// [fieldbehavior.Strip] for OUTPUT_ONLY).
//
// In every mode a path setting a oneof member whose sibling is, or holds, an
// IMMUTABLE or IDENTIFIER field (or a top-level identity, immutable or secret
// field of the spec) is refused when the stored resource holds that sibling,
// or without [WithCurrent] whatever it holds: setting it would clear the
// sibling. An OUTPUT_ONLY sibling is never guarded. Only a switch the merge
// makes is refused: a member the request leaves unset is cleared, which
// switches nothing, and a "*" sub-path of a map reaches the request entries
// no key names, each checked against the stored entry of the same key.
//
// A secret changes only when the request sets it (whatever the non-empty
// mask, "*" included) or the mask names it; "*" never clears a secret the
// request leaves unset. Secrets are the top-level INPUT_ONLY fields only: a
// nested INPUT_ONLY field is an ordinary mutable leaf (implied when set,
// merged by its path, cleared when its parent is replaced), which the caller
// reads from the merged message and protects as it needs. Duplicates are
// dropped. Results follow the declaration order of the fields, map keys in
// sorted order.
//
// The mask limits of the spec ([WithMaxPaths], [WithMaxPathLength],
// [WithMaxPathDepth]) are checked before anything else; a mask over one is
// refused with a cause matching [ErrLimitExceeded].
//
// A plan from [Prepare], supplied with [WithDeferredPlan] or [WithPlanContext],
// is planned from its snapshot and completed against [WithCurrent]. It restores the original
// mask intent even if mask now holds only the normalized effective paths.
// With a bound plan, Spec.Plan and Patch.Run behave as on the client's original
// request and mask, after the boundary's OUTPUT_ONLY sanitization. In addition,
// an update is refused if the boundary's policy refuses it against the stored
// resource or if what would be written changes a value the boundary protects.
// The boundary is evaluated against the supplied stored resource on every call,
// even if Prepare received WithCurrent. Refusals retain the base Plan error shape.
// Only OUTPUT_ONLY values and wholly OUTPUT_ONLY mask paths are removed from
// the captured input, preserving keyed exclusions and explicit empty-mask no-ops.
// Mask errors retain the original client index and spelling, including when
// preserving a keyed exclusion expands a wildcard into concrete paths. Raw-mask
// limits are checked before this sanitization; expanded paths also obey the
// existing limits, with sanitization expansion failures naming their raw path.
// The Spec alone determines paths, secrets and atomic-message handling, exactly
// as in a one-stage call on that input; its planned request is published unchanged.
// The final boundary guard includes secrets supplied outside the mask and their
// oneof side effects, using the boundary's own protected-value equality.
// Without current state, the plan remains interim and must be completed.
// Direct ApplyMask/Complete filtering is unchanged. Policy-only routing identities
// retain their documented rules: the Spec refuses explicit identity paths;
// implied and wildcard updates do not compare or write them.
// Protected-message comparison ignores OUTPUT_ONLY descendants, so a preceding
// [fieldbehavior.StripUpdate] cannot cause a false change. Successful stateful
// planning restores stored protected values inside planned whole replacements
// to resource before merging. Every failure leaves resource unchanged, including
// boundary refusals with a bound plan. The sanitized request is published only
// after planning and the final boundary guard succeed.
// Patch also preserves these values when sanitizing its merge input.
//
// Mask refusals carry a [coreerrs.FieldError] whose Err chain holds a
// *[MaskPathError]: Field is the spec's update-mask field followed by
// ".paths[i]" for the offending path i of the mask ("updateMask.paths[2]",
// the AIP-193 field violation of that path), or the bare update-mask field
// when the refusal comes from the implied or "*" mask, or exceeds the path count.
// A nil resource, one of another descriptor, or a [WithCurrent] message of
// another descriptor is [ErrMessageMismatch]. Plan reports one violation from
// the same ordered list as [ApplyMask]: the base mask/expansion traversal first,
// then newly diagnosed fields in declaration order (see [BehaviorError]).
// Secret oneof checks follow ordinary mask refusals.
// [Patch.Run] wraps a changed protected
// value with a resource-field error. Neither entry point returns an aggregate
// [BehaviorError] or [StrictError].
func (s *Spec) Plan(resource proto.Message, mask *fieldmaskpb.FieldMask, opts ...Option) (Plan, error) {
	if s == nil || !validMessage(resource) || resource.ProtoReflect().Descriptor() != s.desc {
		return Plan{}, ErrMessageMismatch
	}

	o := newOptions(opts...)

	pending, err := o.planFor(resource)
	if err != nil {
		return Plan{}, err
	}

	if pending != nil {
		return s.planDeferred(resource, o, pending)
	}

	work, err := cloneMessage(resource)
	if err != nil {
		return Plan{}, err
	}

	plan, err := s.planResource(work, mask, o)
	if err != nil {
		return Plan{}, err
	}

	if err := publishMessage(resource, work); err != nil {
		return Plan{}, err
	}

	return plan, nil
}

// planResource restores protected values only in a private request.
func (s *Spec) planResource(resource proto.Message, mask *fieldmaskpb.FieldMask, o *options) (Plan, error) {
	if v := s.opts.limits().CheckMask(mask.GetPaths()); v != nil {
		return Plan{}, s.fieldError(limitFieldError(v))
	}

	u, root, err := newUpdateCheck(resource, o.current, s.opts)
	if err != nil {
		return Plan{}, err
	}

	if mask != nil && len(mask.GetPaths()) == 0 {
		return Plan{}, nil
	}

	u.top = s.topBehavior

	var paths []string
	if mask != nil {
		paths = mask.GetPaths()
	}

	p, err := s.plan(u, root, paths, mask == nil)
	if err != nil {
		return Plan{}, s.fieldError(err)
	}

	if root.cur != nil {
		// A prior StripUpdate may have cleared ignored descendants. Restore
		// the compared stored values before a direct caller merges paths.
		var kept []keptValue

		if err := collectKept(fieldmask.FromPaths(p.Paths...), root.req, root.cur, s.opts, &kept); err != nil {
			return Plan{}, err
		}

		for _, k := range kept {
			if err := maskmerge.SetValue(k.parent, k.fd, k.value); err != nil {
				return Plan{}, conversionError(err)
			}
		}
	}

	return p, nil
}

// maskPath is an explicit update mask path: its position in the mask, its
// spelling in the request and its resolved segments (see [resolveUpdateSegments]).
type maskPath struct {
	resolvedLeaf bool // explicitly named leaf, independent of descriptor expansion
	index        int
	raw          string
	segs         []string
}

// plan resolves the mask of [Spec.Plan] and collects all behavior violations,
// including the oneof consequences of secrets. implied selects the implied mask.
func (s *Spec) plan(u *updateCheck, root msgPair, paths []string, implied bool) (Plan, error) {
	var p Plan

	for _, fd := range s.order {
		if root.req.Has(fd) && s.rootClassification(fd).isWriteOnly() {
			p.set = append(p.set, string(fd.Name()))
		}
	}

	var (
		tree     fieldmask.FieldMask
		explicit []maskPath
		covered  []fieldmask.FieldMask // the explicit paths tree covers, each alone
	)

	switch all, err := selectsAll(paths); {
	case implied:
		// The base checked the writable implied tree first, then compared
		// supplied protected fields in a declaration-order expansion.
		u.orderMode = referenceSpecImplied
		u.orderSkip = slices.Concat(s.skip, s.opts.impliedSkipFields)
		tree = u.impliedSelection(root)
		u.checkImplied(s.desc, root)
		u.checkCovering(tree, nil, s.desc, root)
	case err != nil:
		return Plan{}, err
	case all:
		tree = u.replaceAll(s.desc, root)
	default:
		tree = fieldmask.FieldMask{}

		for i, raw := range paths {
			mp, err := s.addExplicitPath(tree, &p, i, raw)
			if err != nil {
				return Plan{}, err
			}

			explicit = append(explicit, mp)
		}

		covered = coveredExplicit(tree, explicit)
		u.checkCovering(tree, covered, s.desc, root)
	}

	if err := u.maskRefusal(tree, s.desc); err != nil {
		return Plan{}, planRefusalError(err, explicit)
	}

	// As at the base, secret oneof checks follow ordinary mask refusals.
	if len(u.violations) == 0 {
		if err := s.checkOneofSwitches(slices.Concat(p.set, p.named), root.cur, explicit, u); err != nil {
			return Plan{}, err
		}
	}

	violations, err := u.orderedViolations(s.desc)
	if err != nil {
		return Plan{}, err
	}

	if len(violations) > 0 {
		return Plan{}, planViolationError(violations[0], explicit)
	}

	p.Paths = declaredPaths(tree, s.desc)

	return p, nil
}

// coveredExplicit returns the single-path masks of the explicit paths that
// a shorter explicit path covers in tree ("profile.id" under "profile"), for
// [updateCheck.checkCovering].
func coveredExplicit(tree fieldmask.FieldMask, explicit []maskPath) []fieldmask.FieldMask {
	paths := make([][]string, len(explicit))
	for i, mp := range explicit {
		paths[i] = mp.segs
	}

	return coveredMasks(tree, paths)
}

// topBehavior is the [topBehavior] of the spec's classes: a secret and an
// identity field without an IDENTIFIER annotation (id, etag) are neither
// written nor checked by the update; an output-only, identity or immutable
// class governs as OUTPUT_ONLY, IDENTIFIER or IMMUTABLE.
func (s *Spec) topBehavior(fd protoreflect.FieldDescriptor) (annotations.FieldBehavior, bool) {
	f := s.rootClassification(fd)
	if f.isWriteOnly() || f.namedIdentity && !f.outputOnly && !f.identifier {
		return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, true
	}

	return f.managedBehavior(), false
}

// addExplicitPath adds raw, the mask path at index i, to tree: a path of a
// top-level field to the tree, a secret to the named secrets of p. An
// unknown or malformed path, a path below a secret and a skipped identity
// field are refused.
func (s *Spec) addExplicitPath(tree fieldmask.FieldMask, p *Plan, i int, raw string) (maskPath, error) {
	segs, reason := resolveUpdateSegments(s.desc, raw, s.opts)
	if reason != "" {
		return maskPath{}, indexedFieldError(fieldmask.ErrInvalidPath, i, raw, "", reason)
	}

	mp := maskPath{index: i, raw: raw, segs: segs}
	top := segs[0]

	switch s.class(top) {
	case classSecret:
		if len(segs) > 1 {
			return maskPath{}, indexedFieldError(fieldmask.ErrInvalidPath, i, raw, "", fmt.Sprintf("unknown path %q", raw))
		}

		if !slices.Contains(p.named, top) {
			p.named = append(p.named, top)
		}
	case classIdentity:
		if _, skip := s.topBehavior(s.desc.Fields().ByName(protoreflect.Name(top))); skip {
			return maskPath{}, indexedFieldError(ErrProtectedField, i, raw, top, fmt.Sprintf("path %q cannot be updated", raw))
		}

		maskwalk.Insert(tree, segs)
	default:
		maskwalk.Insert(tree, segs)
	}

	return mp, nil
}

// checkPaths reports the refusals of the explicit mask paths that need no
// resource value: a malformed path, "*" with other paths, an unknown path, a
// path below a secret and a skipped identity field.
func (s *Spec) checkPaths(paths []string) error {
	if v := s.opts.limits().CheckMask(paths); v != nil {
		return s.fieldError(limitFieldError(v))
	}

	if all, err := selectsAll(paths); err != nil || all {
		return s.fieldError(err)
	}

	var p Plan

	tree := fieldmask.FieldMask{}
	for i, raw := range paths {
		if _, err := s.addExplicitPath(tree, &p, i, raw); err != nil {
			return s.fieldError(err)
		}
	}

	return nil
}

// declaredPaths returns the paths of tree, a mask of md, in the declaration
// order of the fields at every level and the sorted order of the keys (map
// keys and "*") below a repeated or map field. Map keys are quoted as
// [fieldmask.FieldMask.ToPaths] quotes them.
func declaredPaths(tree fieldmask.FieldMask, md protoreflect.MessageDescriptor) []string {
	var out []string

	var walk func(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, prefix string)

	walk = func(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, prefix string) {
		if maskpath.IsDynamicMessage(md) {
			out = append(out, dynamicPaths(msk, prefix)...)
			return
		}

		fields := md.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)

			nested, ok := msk[string(fd.Name())]
			if !ok {
				continue
			}

			path := behavior.JoinPath(prefix, string(fd.Name()))

			switch {
			case nested == nil || fd.Message() == nil:
				out = append(out, path)
			case fd.IsList() || fd.IsMap():
				for _, key := range slices.Sorted(maps.Keys(nested)) {
					keyPath := behavior.JoinPath(path, maskpath.PathSegment(key))
					if sub := nested[key]; sub != nil && maskpath.ValueMessage(fd) != nil {
						walk(sub, maskpath.ValueMessage(fd), keyPath)
					} else {
						out = append(out, keyPath)
					}
				}
			default:
				walk(nested, fd.Message(), path)
			}
		}
	}

	walk(tree, md, "")

	return out
}

// dynamicPaths returns the paths of msk, a mask of dynamic JSON below
// prefix, keys (and "*") in sorted order at every level.
func dynamicPaths(msk fieldmask.FieldMask, prefix string) []string {
	var out []string

	keys := slices.SortedFunc(maps.Keys(msk), func(a, b string) int {
		if order := strings.Compare(maskpath.JSONKey(a), maskpath.JSONKey(b)); order != 0 {
			return order
		}

		return strings.Compare(a, b) // distinguish a wildcard from a literal key
	})

	for _, key := range keys {
		path := behavior.JoinPath(prefix, maskpath.PathSegment(key))
		if sub := msk[key]; sub != nil {
			out = append(out, dynamicPaths(sub, path)...)
		} else {
			out = append(out, path)
		}
	}

	return out
}

// holdsImmutable reports whether md holds an IMMUTABLE or IDENTIFIER field
// (one not also OUTPUT_ONLY) directly or in any message reachable through
// its fields — singular, repeated or map values — at any depth, never below
// an OUTPUT_ONLY field, which the server owns whole. visited breaks recursion
// on self-referential schemas.
func holdsImmutable(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options) bool {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		switch policy.serverManaged(fd) { //nolint:exhaustive // serverManaged returns only these or UNSPECIFIED.
		case annotations.FieldBehavior_OUTPUT_ONLY:
			continue // the server owns it and everything below it
		case annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE:
			return true
		}

		sub := maskpath.ValueMessage(fd)

		switch {
		case sub == nil:
		case isProtectedAtomicType(sub, policy):
			return true // a protected leaf
		case policy.atomicMessage != nil && policy.atomicMessage(sub):
		case !visited[sub.FullName()] && holdsImmutable(sub, visited, policy):
			return true
		}
	}

	return false
}

// isImmutableBehavior reports whether b is IMMUTABLE or IDENTIFIER.
func isImmutableBehavior(b annotations.FieldBehavior) bool {
	return b == annotations.FieldBehavior_IMMUTABLE || b == annotations.FieldBehavior_IDENTIFIER
}

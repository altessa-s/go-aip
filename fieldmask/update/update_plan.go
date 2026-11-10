// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"maps"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// Plan is the resolved update mask of one request.
type Plan struct {
	// Paths are the declared mutable paths merged from the request, as
	// [MergePaths] takes them: a field path replaces the field, a nested path
	// one leaf, a map key path one entry, and a sub-path through a map key or
	// "*" the fields of the elements it addresses. Empty for a no-op.
	Paths []string
	// set lists the secrets the request populates; named lists the secrets
	// the mask names explicitly.
	set, named []string
}

// Empty reports that the update changes nothing.
func (p Plan) Empty() bool {
	return len(p.Paths) == 0 && len(p.set) == 0 && len(p.named) == 0
}

// Secret returns the service input of the write-only field name: value when
// the request sets it, an empty string (clear) when the mask names it unset,
// and nil (keep the stored value) otherwise.
func (p Plan) Secret(name string, value *string) *string {
	switch {
	case slices.Contains(p.set, name):
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
//     checked as if sent alone and refused on its own index.
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
//     ([ErrProtectedField]). Without [WithCurrent] it is refused, except a set
//     IDENTIFIER the mask does not name, which is kept as the address. One
//     the resource leaves unset in the implied and "*" modes or a replaced
//     message keeps its stored value. An element updated in place through a
//     sub-path is compared with its stored element the same way.
//   - REQUIRED: a path naming a REQUIRED field the resource leaves absent or
//     falsy is refused ([ErrBehaviorViolation]), and so is a REQUIRED field
//     left empty in a value the update writes whole: a replaced message
//     (recursing only into its populated sub-messages), every element of a
//     replaced collection or map entry, and resource itself under "*".
//
// A collection or map entry replaced whole holds new values, so their
// IMMUTABLE and IDENTIFIER values are not compared, and their OUTPUT_ONLY
// values are cleared from the request before the merge ([Patch] does; a
// direct [MergePaths] caller clears them first, with [fieldbehavior.Strip]
// for OUTPUT_ONLY, say).
//
// In every mode a path setting a oneof member whose sibling is or holds a
// server-managed field is refused (setting it would clear that sibling); in
// the elements a "*" sub-path addresses, every stored element is checked.
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
// Every refusal is a [coreerrs.FieldError] whose Err chain holds a
// *[MaskPathError]: Field is the spec's update-mask field followed by
// ".paths[i]" for the offending path i of the mask ("updateMask.paths[2]",
// the AIP-193 field violation of that path), or the bare update-mask field
// when the refusal comes from the implied or "*" mask. A nil resource, one
// of another type, or a [WithCurrent] message of another type is
// [ErrMessageMismatch].
func (s *Spec) Plan(resource proto.Message, mask *fieldmaskpb.FieldMask, opts ...Option) (Plan, error) {
	if s == nil || resource == nil || !resource.ProtoReflect().IsValid() || resource.ProtoReflect().Descriptor() != s.desc {
		return Plan{}, ErrMessageMismatch
	}

	if v := s.opts.limits().CheckMask(mask.GetPaths()); v != nil {
		return Plan{}, s.fieldError(limitFieldError(v))
	}

	u, root, err := newUpdateCheck(resource, newOptions(opts...).current, s.opts)
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

	p, explicit, err := s.plan(u, root, paths, mask == nil)
	if err != nil {
		return Plan{}, s.fieldError(err)
	}
	// A secret the request sets or the mask names is written too: it must
	// not switch a oneof away from a guarded member either.
	if err = s.checkOneofSwitches(slices.Concat(p.set, p.named), root.cur, explicit); err != nil {
		return Plan{}, s.fieldError(err)
	}

	return p, nil
}

// maskPath is an explicit update mask path: its position in the mask, its
// spelling in the request and its resolved segments (see [resolveUpdateSegments]).
type maskPath struct {
	index int
	raw   string
	segs  []string
}

// plan resolves the mask of [Spec.Plan] before the oneof check of the
// secrets it sets or clears; implied selects the implied mask. It returns
// the explicit paths too, which locate later refusals in the mask.
func (s *Spec) plan(u *updateCheck, root msgPair, paths []string, implied bool) (Plan, []maskPath, error) {
	var p Plan

	for _, fd := range s.order {
		if root.req.Has(fd) && s.class(string(fd.Name())) == classSecret {
			p.set = append(p.set, string(fd.Name()))
		}
	}

	var (
		tree     fieldmask.FieldMask
		explicit []maskPath
	)

	switch all, err := selectsAll(paths); {
	case implied:
		tree = fieldmask.FromPaths(impliedPaths(root.req.Interface(), slices.Concat(s.skip, s.opts.impliedSkipFields), s.opts)...)
		u.checkMask(tree, s.desc, []msgPair{root}, "")
		// The implied mask covers the protected fields resource sets: the
		// expansion of the root compares them and is otherwise discarded.
		u.expand(s.desc, []msgPair{root}, "", map[protoreflect.FullName]bool{s.desc.FullName(): true})
	case err != nil:
		return Plan{}, nil, err
	case all:
		tree = u.replaceAll(s.desc, root)
	default:
		tree = fieldmask.FieldMask{}

		for i, raw := range paths {
			mp, err := s.addExplicitPath(tree, &p, i, raw)
			if err != nil {
				return Plan{}, nil, err
			}

			explicit = append(explicit, mp)
		}

		u.checkCovering(tree, coveredExplicit(tree, explicit), s.desc, root)
	}

	if len(u.violations) > 0 {
		return Plan{}, nil, planViolationError(u.violations[0], explicit)
	}

	if root.cur != nil {
		if err := checkElementCounts(root.cur, root.req, tree, s.desc, ""); err != nil {
			return Plan{}, nil, planPathError(ErrElementCountMismatch, err.Path, explicit, err.Reason)
		}
	}

	p.Paths = declaredPaths(tree, s.desc)

	return p, explicit, s.checkOneofSwitches(p.Paths, root.cur, explicit)
}

// coveredExplicit returns the single-path masks of the explicit paths that
// a shorter explicit path covers in tree ("profile.id" under "profile"), for
// [updateCheck.checkCovering].
func coveredExplicit(tree fieldmask.FieldMask, explicit []maskPath) []fieldmask.FieldMask {
	var covered []fieldmask.FieldMask

	for _, mp := range explicit {
		if maskwalk.CoveredAt(tree, mp.segs) > 0 {
			single := fieldmask.FieldMask{}
			maskwalk.Insert(single, mp.segs)
			covered = append(covered, single)
		}
	}

	return covered
}

// topBehavior is the [topBehavior] of the spec's classes: a secret and an
// identity field without an IDENTIFIER annotation (id, etag) are neither
// written nor checked by the update; an output-only, identity or immutable
// class governs as OUTPUT_ONLY, IDENTIFIER or IMMUTABLE.
func (s *Spec) topBehavior(fd protoreflect.FieldDescriptor) (annotations.FieldBehavior, bool) {
	managed := s.opts.serverManaged(fd)

	switch s.class(string(fd.Name())) {
	case classSecret:
		return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, true
	case classIdentity:
		switch {
		case managed == annotations.FieldBehavior_OUTPUT_ONLY:
			return managed, false
		case slices.Contains(s.opts.behaviors(fd), annotations.FieldBehavior_IDENTIFIER):
			return annotations.FieldBehavior_IDENTIFIER, false
		default:
			return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, true
		}
	case classOutputOnly:
		return annotations.FieldBehavior_OUTPUT_ONLY, false
	case classImmutable:
		return annotations.FieldBehavior_IMMUTABLE, false
	default:
		return managed, false
	}
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
					keyPath := behavior.JoinPath(path, behavior.QuoteSegment(key))
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

	for _, key := range slices.Sorted(maps.Keys(msk)) {
		path := behavior.JoinPath(prefix, behavior.QuoteSegment(key))
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
// its fields — singular, repeated or map values — at any depth. visited
// breaks recursion on self-referential schemas.
func holdsImmutable(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, policy *options) bool {
	visited[md.FullName()] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if isImmutableBehavior(policy.serverManaged(fd)) {
			return true
		}

		if sub := maskpath.ValueMessage(fd); sub != nil && (policy.atomicMessage == nil || !policy.atomicMessage(sub)) && !visited[sub.FullName()] &&
			holdsImmutable(sub, visited, policy) {
			return true
		}
	}

	return false
}

// isImmutableBehavior reports whether b is IMMUTABLE or IDENTIFIER.
func isImmutableBehavior(b annotations.FieldBehavior) bool {
	return b == annotations.FieldBehavior_IMMUTABLE || b == annotations.FieldBehavior_IDENTIFIER
}

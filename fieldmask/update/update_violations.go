// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

type referenceOrderMode int

const (
	referenceExplicit referenceOrderMode = iota
	referenceReplacement
	referenceImplied
	referenceSpecImplied
)

// referenceDiagnostic records one report's actual selection, before another
// branch or covered mask can select different elements under the same path.
type referenceDiagnostic struct {
	violation FieldViolation
	paths     []string
}

func (u *baseOrderUpdateCheck) recordReference(v FieldViolation, paths ...string) {
	if paths == nil {
		paths = []string{v.Path}
	}

	u.diagnostics = append(u.diagnostics, referenceDiagnostic{violation: v, paths: paths})
}

// referencePairPaths records the complete selection group, including elements
// after the first difference where the base stops. The evaluation determines
// which of these concrete fields actually violated the behavior.
func referencePairPaths(fd protoreflect.FieldDescriptor, pairs []baseOrderMsgPair) []string {
	paths := make([]string, len(pairs))
	for i, p := range pairs {
		paths[i] = behavior.JoinPath(p.path, string(fd.Name()))
	}

	return paths
}

// referenceViolations runs the frozen base walk against private mask copies.
// Resource values are read only. Dynamic roots have no base selection semantics;
// a selection the base refuses (limits or wildcard pinning) cannot supply an order.
// Both use the deterministic fallback alone. Deferred completion supplies the
// original selection and values, so it uses exactly the same reference walk.
func (u *updateCheck) referenceViolations(md protoreflect.MessageDescriptor) (diagnostics []referenceDiagnostic, err error) {
	// Unsupported reference traversals retain deterministic diagnostic order.
	// A conversion failure is different: it must reach the public caller.
	var ref *baseOrderUpdateCheck

	defer func() {
		if recover() != nil {
			diagnostics = nil

			if ref != nil && errors.Is(ref.refusal, ErrMessageMismatch) {
				err = ref.refusal
			}
		}
	}()

	if maskpath.IsDynamicMessage(md) {
		return nil, nil
	}

	ref = &baseOrderUpdateCheck{policy: u.policy, verify: u.verify, top: u.top, addressOnly: u.addressOnly}
	root := baseOrderMsgPair{req: u.orderRoot.req, cur: u.orderRoot.cur}

	var work fieldmask.FieldMask

	switch u.orderMode {
	case referenceReplacement:
		work = ref.replaceAll(md, root)
	case referenceImplied:
		skip, descend := baseOrderSynthesisFields(root.req, u.policy)
		tree := fieldmask.FromPaths(baseOrderCollect(root.req, true,
			baseOrderSetFields{Skip: skip, Descend: descend}, u.policy.maxPathDepth)...)
		work = tree
		// The base Apply entry points returned immediately for an empty implied mask.
		if len(tree) > 0 {
			ref.checkMask(tree, md, []baseOrderMsgPair{root}, "")
		}
	case referenceSpecImplied:
		tree := fieldmask.FromPaths(baseOrderImpliedPaths(root.req.Interface(), u.orderSkip, u.policy)...)
		ref.checkMask(tree, md, []baseOrderMsgPair{root}, "")
		work = tree
		ref.validationOnly = true
		ref.expand(md, []baseOrderMsgPair{root}, "", map[protoreflect.FullName]bool{md.FullName(): true})
		ref.validationOnly = false
	case referenceExplicit:
		if len(u.violationOrder) == 0 {
			return nil, nil
		}

		trees := make([]fieldmask.FieldMask, len(u.violationOrder))
		for i, tree := range u.violationOrder {
			// Literal JSON star keys were not selections at the base.
			if referenceHasLiteralStar(tree) {
				return nil, nil
			}

			trees[i] = tree.Clone()
		}

		ref.checkCovering(trees[0], trees[1:], md, root)
		work = trees[0]
	}

	if errors.Is(ref.refusal, ErrMessageMismatch) {
		return nil, ref.refusal
	}

	if ref.maskRefusal(work, md) != nil {
		return nil, nil
	}

	return ref.diagnostics, nil
}

// referenceHasLiteralStar identifies selections added after the base.
func referenceHasLiteralStar(tree fieldmask.FieldMask) bool {
	for key, sub := range tree {
		if key == maskpath.LiteralStar || referenceHasLiteralStar(sub) {
			return true
		}
	}

	return false
}

// orderedViolations is the only diagnostic-ordering boundary. Acceptance comes
// exclusively from the evaluation: reference-only diagnostics are discarded.
// The reference prefix is followed by remaining violations in field declaration
// order, numeric list-index order and unescaped map-key order, then behavior and
// reason to break ties. Each reference diagnostic joins only its recorded
// concrete selection, in reference element order, preserving keyed exclusions
// and each covered mask's selection. No path pattern is matched after the walk.
func (u *updateCheck) orderedViolations(md protoreflect.MessageDescriptor) ([]FieldViolation, error) {
	if len(u.violations) == 0 {
		return nil, nil
	}

	reference, err := u.referenceViolations(md)
	if err != nil {
		return nil, err
	}

	keys := make(map[FieldViolation][]violationPosition, len(u.violations))

	found := make(map[FieldViolation]bool, len(u.violations))
	for _, v := range u.violations {
		keys[v] = declarationKey(md, maskpath.Split(v.Path))
		found[v] = true
	}

	slices.SortFunc(u.violations, func(a, b FieldViolation) int {
		order := slices.CompareFunc(keys[a], keys[b], func(a, b violationPosition) int {
			if order := cmp.Compare(a.index, b.index); order != 0 {
				return order
			}

			return strings.Compare(a.key, b.key)
		})
		if order != 0 {
			return order
		}

		if order := cmp.Compare(a.Behavior, b.Behavior); order != 0 {
			return order
		}

		return strings.Compare(a.Reason, b.Reason)
	})

	ordered := make([]FieldViolation, 0, len(u.violations))

	for _, diagnostic := range reference {
		for _, path := range diagnostic.paths {
			v := diagnostic.violation

			v.Path = path
			if found[v] {
				ordered = append(ordered, v)
				delete(found, v)
			}
		}
	}

	for _, v := range u.violations {
		if found[v] {
			ordered = append(ordered, v)
		}
	}

	u.violations = ordered

	return ordered, nil
}

// violationPosition is a descriptor's declaration index, a numeric list index,
// or an unescaped map key. The path's schema fixes the kind at each position.
type violationPosition struct {
	index int
	key   string
}

// declarationKey resolves only the finite path of an existing violation, so
// recursive descriptors cannot cause an unbounded schema walk. Unknown segments
// retain lexical ordering; validation normally reports only declared fields.
func declarationKey(md protoreflect.MessageDescriptor, segments []string) []violationPosition {
	key := make([]violationPosition, 0, len(segments))

	for i := 0; i < len(segments); i++ {
		var fd protoreflect.FieldDescriptor
		if md != nil {
			fd = md.Fields().ByName(protoreflect.Name(segments[i]))
		}

		if fd == nil {
			key = append(key, violationPosition{key: segments[i]})
			md = nil

			continue
		}

		key = append(key, violationPosition{index: fd.Index()})
		md = maskpath.ValueMessage(fd)

		if (fd.IsList() || fd.IsMap()) && i+1 < len(segments) {
			i++
			key = append(key, elementPosition(fd, segments[i]))
		}
	}

	return key
}

// elementPosition orders list elements numerically and map keys by their
// unescaped spelling, as elementPairs and behavior.SortedMapKeys did at the base.
func elementPosition(fd protoreflect.FieldDescriptor, segment string) violationPosition {
	if fd.IsList() {
		if index, err := strconv.Atoi(segment); err == nil {
			return violationPosition{index: index}
		}
	}

	if segment == maskpath.LiteralStar {
		segment = maskpath.Wildcard
	}

	return violationPosition{key: segment}
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// ListIndex places a repeated-field index within a [ResolvedViolation.Path].
type ListIndex struct {
	// Segment is the zero-based path segment naming the repeated field.
	Segment int
	// Index is the zero-based position in that field.
	Index int
}

// MapKey places a concrete protobuf map key within a [ResolvedViolation.Path].
type MapKey struct {
	// Segment is the zero-based path segment naming the map field.
	Segment int
	// Key is the unescaped key in its protobuf string representation.
	Key string
}

// ResolvedViolation describes a failed value check on a resolved mask.
type ResolvedViolation struct {
	// Path is resource-relative. It uses declared field names and dot-separated
	// map keys, with AIP-161 backtick quoting for keys such as `a.b` and `*`.
	// Repeated-field indexes appear separately in ListIndexes.
	Path string
	// ListIndexes holds repeated-field positions in path order.
	ListIndexes []ListIndex
	// MapKeys holds concrete map keys in path order.
	MapKeys []MapKey
	// Index is the original explicit mask index or [SynthesizedPathIndex].
	Index int
	// Behavior is the field behavior that triggered the violation.
	Behavior annotations.FieldBehavior
	// Reason describes the violation.
	Reason string
}

// IndexedPath renders indexes as brackets and map keys as quoted Go strings
// in brackets, for example members[1].email or delegates["a.b"].email.
func (v ResolvedViolation) IndexedPath() string {
	if len(v.ListIndexes) == 0 && len(v.MapKeys) == 0 {
		return v.Path
	}

	var path strings.Builder

	indexes, keys := v.ListIndexes, v.MapKeys

	skipKey := false
	for i, seg := range maskpath.Split(v.Path) {
		if skipKey {
			skipKey = false
			continue
		}

		if i > 0 {
			path.WriteByte('.')
		}

		path.WriteString(maskpath.PathSegment(seg))

		if len(keys) > 0 && keys[0].Segment == i {
			path.WriteByte('[')
			path.WriteString(strconv.Quote(keys[0].Key))
			path.WriteByte(']')

			keys = keys[1:]
			skipKey = true
		}

		if len(indexes) > 0 && indexes[0].Segment == i {
			path.WriteByte('[')
			path.WriteString(strconv.Itoa(indexes[0].Index))
			path.WriteByte(']')

			indexes = indexes[1:]
		}
	}

	return path.String()
}

// ValidateResolved checks REQUIRED and protected fields for a mask resolved by
// this spec on the original request resource. Pass [WithCurrent] to compare
// protected values against stored state. For an omitted mask, protected input
// retains its own comparison scope, including collections and atomic values
// excluded by an allow-list. Explicit leaves retain their checks during oneof
// expansion. Covered collection paths retain their element-count checks.
//
// Configure [WithAllowedPaths] at [NewSpec]. Supplying it here returns
// [ErrAllowedPathsAtCall]. Other call options are ignored. Violations appear in
// descriptor order. The request, stored resource and selection stay untouched.
func (s *Spec) ValidateResolved(resource proto.Message, selection ResolvedMask, opts ...Option) ([]ResolvedViolation, error) {
	if s == nil || selection.spec != s || !validMessage(resource) || resource.ProtoReflect().Descriptor() != s.desc {
		return nil, ErrMessageMismatch
	}

	var current proto.Message

	if len(opts) != 0 {
		var call options
		for _, opt := range opts {
			opt(&call)
		}

		if call.allowedPaths != nil {
			return nil, ErrAllowedPathsAtCall
		}

		current = call.current
	}

	if len(selection.tree) == 0 && len(selection.whole) == 0 && !selection.implied {
		return nil, nil
	}

	root := msgPair{req: resource.ProtoReflect()}
	if current != nil {
		if _, err := reflectPair(resource, current); err != nil {
			return nil, err
		}

		root.cur = current.ProtoReflect()
	}

	u := updateCheck{
		policy: s.opts, top: s.topBehavior, verify: current != nil,
		resolved: true, resolvedWhole: selection.whole, resolvedOneofs: selection.oneofs,
	}

	tree := selection.tree.Clone()
	if tree == nil {
		tree = fieldmask.FieldMask{}
	}

	effect := u.resolvedEffect(selection, tree)

	root = u.normalizeResolvedPair(root, tree, effect, selection.implied)
	if u.refusal != nil {
		return nil, u.refusal
	}

	if selection.implied && u.verify {
		u.checkImplied(s.desc, root)
	}

	if len(effect) != 0 && u.refusal == nil {
		u.evaluateSelection(effect, root)
	}

	if u.refusal == nil {
		u.checkSelectionInputs(tree, coveredExplicit(u.namedInput, selection.explicit), s.desc, root)
		u.checkExplicitResolvedLeaves(selection, root)
	}

	if refusal := u.maskRefusal(tree, s.desc); refusal != nil {
		if _, ok := errors.AsType[*elementCountError](refusal); ok {
			return nil, s.fieldError(planRefusalError(refusal, selection.explicit))
		}

		return nil, refusal
	}

	sortViolations(u.violations, s.desc)

	var out []ResolvedViolation

	for _, violation := range u.violations {
		resolved := resolveViolation(s.desc, violation)
		resolved.Index, _ = maskIndex(selection.explicit, violation.Path)
		out = append(out, resolved)
	}

	return out, nil
}

// resolvedEffect keeps whole explicit selections for comparisons that depend
// on replacing the complete value, including its protobuf unknown fields.
func (u *updateCheck) resolvedEffect(selection ResolvedMask, tree fieldmask.FieldMask) fieldmask.FieldMask {
	switch {
	case selection.implied:
		return selection.protected.Clone()
	case selection.all && selection.spec.allowed == nil:
		return u.replacementSelection(selection.spec.desc)
	default:
		var original fieldmask.FieldMask

		for _, path := range selection.explicit {
			if path.resolvedLeaf {
				continue
			}

			if original == nil {
				original = tree.Clone()
			}

			maskwalk.Insert(original, path.segs)
		}

		if original != nil {
			return original
		}

		return tree
	}
}

// addResolvedMessageChecks adds non-terminal validation nodes after the write
// effects have been checked. A node can check both message presence and leaves.
func (u *updateCheck) addResolvedMessageChecks(tree fieldmask.FieldMask) {
	for path := range u.resolvedWhole {
		node := tree
		for _, segment := range maskpath.Split(path) {
			child, named := node[segment]
			if named && child == nil {
				break // whole-value selection already checks this subtree
			}

			if !named {
				child = fieldmask.FieldMask{}
				node[segment] = child
			}

			node = child
		}
	}
}

// Explicit leaves keep their own input check when an overlapping message
// expansion skips an inactive oneof branch.
func (u *updateCheck) checkExplicitResolvedLeaves(selection ResolvedMask, root msgPair) {
	if u.refusal != nil || len(selection.oneofs) == 0 {
		return
	}

	u.resolvedOneofs = nil

	for _, leaf := range selection.explicit {
		if !leaf.resolvedLeaf {
			continue
		}

		expanded := false

		for i := 1; i < len(leaf.segs); i++ {
			if selection.oneofs[maskpath.Join(leaf.segs[:i])] {
				expanded = true
				break
			}
		}

		if expanded {
			tree := fieldmask.FieldMask{}
			maskwalk.Insert(tree, leaf.segs)
			u.checkMask(tree, selection.spec.desc, []msgPair{root}, "")
		}
	}
}

func (u *updateCheck) resolvedFieldPairs(fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string) []msgPair {
	if nested != nil && u.resolvedWhole[path] {
		pairs = u.resolvedRequiredPairs(fd, pairs)
	}

	if nested != nil && u.resolvedOneofs[path] {
		return activeResolvedPairs(pairs, fd)
	}

	return pairs
}

// activeResolvedPairs keeps only the request's active branch during expansion.
func activeResolvedPairs(pairs []msgPair, fd protoreflect.FieldDescriptor) []msgPair {
	if len(pairs) == 1 && pairs[0].req != nil && pairs[0].req.Has(fd) {
		return pairs
	}

	var active []msgPair

	for _, pair := range pairs {
		if pair.req != nil && pair.req.Has(fd) {
			active = append(active, pair)
		}
	}

	return active
}

// resolvedRequiredPairs checks the selected message itself before its leaves.
// A missing required message suppresses descendant checks for that value.
func (u *updateCheck) resolvedRequiredPairs(fd protoreflect.FieldDescriptor, pairs []msgPair) []msgPair {
	if len(pairs) == 1 {
		if u.checkRequired(fd, pairs) {
			return nil
		}

		return pairs
	}

	var present []msgPair

	for i := range pairs {
		if !u.checkRequired(fd, pairs[i:i+1]) {
			present = append(present, pairs[i])
		}
	}

	return present
}

// resolveViolation decodes the checker's concrete path using the descriptor.
// Numeric map keys and list indexes have the same spelling in legacy paths.
func resolveViolation(md protoreflect.MessageDescriptor, v FieldViolation) ResolvedViolation {
	out := ResolvedViolation{Behavior: v.Behavior, Reason: v.Reason}
	segs := maskpath.Split(v.Path)
	path := make([]string, 0, len(segs))

	for i := 0; i < len(segs); i++ {
		path = append(path, segs[i])

		if md == nil {
			continue
		}

		fd := md.Fields().ByName(protoreflect.Name(segs[i]))
		if fd == nil {
			md = nil
			continue
		}

		if fd.IsList() && i+1 < len(segs) {
			i++
			if index, err := strconv.Atoi(segs[i]); err == nil {
				out.ListIndexes = append(out.ListIndexes, ListIndex{Segment: len(path) - 1, Index: index})
			} else {
				path = append(path, segs[i])
			}
		}

		if fd.IsMap() && i+1 < len(segs) {
			i++
			key := maskpath.JSONKey(segs[i])
			out.MapKeys = append(out.MapKeys, MapKey{Segment: len(path) - 1, Key: key})

			segment := segs[i]
			if key == maskpath.Wildcard {
				segment = maskpath.LiteralStar
			}

			path = append(path, segment)
		}

		md = maskpath.ValueMessage(fd)
	}

	out.Path = maskpath.Join(path)

	return out
}

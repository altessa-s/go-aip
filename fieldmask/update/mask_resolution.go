// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// SynthesizedPathIndex is the origin index for paths selected by an omitted mask or "*".
const SynthesizedPathIndex = -1

// ResolvedPath identifies a canonical resource path and its origin in the sent mask.
type ResolvedPath struct {
	// Path uses the declared protobuf field names.
	Path string
	// Index is the zero-based explicit mask entry or [SynthesizedPathIndex].
	// Overlaps use the most specific entry and duplicates use the first entry.
	Index int
}

// ResolvedMask is the original request selection returned by [Spec.ResolveMask].
type ResolvedMask struct {
	spec      *Spec
	paths     []ResolvedPath
	tree      fieldmask.FieldMask
	whole     map[string]bool
	oneofs    map[string]bool
	explicit  []maskPath
	protected fieldmask.FieldMask
	all       bool
	implied   bool
}

// Paths returns a copy of the canonical validation scope in descriptor declaration
// order, with collection keys in sorted order. Use the original mask for write
// planning so that selecting a whole message still replaces it as a unit.
func (m ResolvedMask) Paths() []ResolvedPath { return slices.Clone(m.paths) }

// ResolveMask resolves the original request mask without checking field values or
// comparing stored state. An omitted mask selects populated leaves, an empty mask
// selects nothing, and "*" selects by descriptor. Explicit paths retain clear
// intent, including below absent messages. Whole singular messages expand into
// their leaf paths. Lists, whole maps, atomic messages, dynamic JSON values and
// recursive branches of unrestricted descriptor expansion are kept whole.
// A partial map allow-list expands its configured keys and descendants for
// "*". Omitted masks select only the populated paths in that scope.
//
// Configure [WithAllowedPaths] at [NewSpec]. Supplying it here returns
// [ErrAllowedPathsAtCall]. Other call options are ignored. Request mask refusals
// carry a [MaskPathError].
func (s *Spec) ResolveMask(resource proto.Message, mask *fieldmaskpb.FieldMask, opts ...Option) (ResolvedMask, error) {
	if s == nil || !validMessage(resource) || resource.ProtoReflect().Descriptor() != s.desc {
		return ResolvedMask{}, ErrMessageMismatch
	}

	if len(opts) != 0 && newOptions(opts...).allowedPaths != nil {
		return ResolvedMask{}, ErrAllowedPathsAtCall
	}

	raw := mask.GetPaths()
	if v := s.opts.limits().CheckMask(raw); v != nil {
		return ResolvedMask{}, s.fieldError(limitFieldError(v))
	}

	all, err := selectsAll(raw)
	if err != nil {
		return ResolvedMask{}, s.fieldError(err)
	}

	r := maskResolver{spec: s, origins: make(map[string]pathOrigin)}
	if err := r.resolve(resource, raw, mask == nil, all); err != nil {
		if mask == nil || all {
			err = indexedFieldError(err, SynthesizedPathIndex, "", "", err.Error())
		}

		return ResolvedMask{}, s.fieldError(err)
	}

	return r.result(mask == nil, all), nil
}

type pathOrigin struct {
	index int
	depth int
}

// allowedMaskPaths is compiled at spec construction and read only thereafter.
type allowedMaskPaths struct {
	explicit map[string]bool
	paths    map[string]bool
	prefixes map[string]bool
	subtrees map[string]bool
	children map[string][]string
}

func (s *Spec) compileAllowedPaths(paths []string) (*allowedMaskPaths, error) {
	a := &allowedMaskPaths{
		explicit: make(map[string]bool, len(paths)),
		paths:    make(map[string]bool, len(paths)),
		prefixes: make(map[string]bool),
		children: make(map[string][]string),
	}
	parents := make(map[string]bool)

	for _, path := range paths {
		if v := s.opts.limits().CheckPath(path); v != nil {
			return nil, fmt.Errorf("invalid allowed path %q: %s: %w", path, v.Reason, v.Cause)
		}

		segs, reason := resolveUpdateSegments(s.desc, path, s.opts)
		if reason != "" {
			return nil, fmt.Errorf("invalid allowed path %q: %s: %w", path, reason, fieldmask.ErrInvalidPath)
		}

		depth := maskpath.Depth(path)

		segs, target := s.resolutionTarget(segs, depth)
		if target.listDepth != 0 && depth > target.listDepth {
			return nil, fmt.Errorf("invalid allowed path %q: list elements cannot be named: %w", path, fieldmask.ErrInvalidPath)
		}

		if target.outputOnly {
			return nil, fmt.Errorf("invalid allowed path %q: OUTPUT_ONLY field: %w", path, fieldmask.ErrInvalidPath)
		}

		canonical := maskpath.Join(segs)
		a.paths[canonical] = true

		// Keep trailing wildcards in the explicit spelling even when resolution
		// folds them into a whole-field selection.
		a.explicit[canonical+strings.Repeat(".*", depth-len(segs))] = true
		if target.message != nil {
			parents[canonical] = true
		}

		for i := 1; i < len(segs); i++ {
			parent := maskpath.Join(segs[:i])

			a.prefixes[parent] = true
			if !slices.Contains(a.children[parent], segs[i]) {
				a.children[parent] = append(a.children[parent], segs[i])
			}
		}
	}

	for path := range parents {
		if !a.prefixes[path] {
			if a.subtrees == nil {
				a.subtrees = make(map[string]bool)
			}

			a.subtrees[path] = true
		}
	}

	for _, children := range a.children {
		slices.Sort(children)
	}

	return a, nil
}

type maskResolver struct {
	spec      *Spec
	origins   map[string]pathOrigin
	whole     map[string]bool
	oneofs    map[string]bool
	explicit  []maskPath
	protected fieldmask.FieldMask
	seen      map[string]bool
	lists     map[string]pathOrigin
	visits    int
}

func (a *allowedMaskPaths) permits(path string) bool {
	if a == nil || a.paths[path] {
		return true
	}

	if len(a.subtrees) != 0 {
		segs := maskpath.Split(path)
		for i := 1; i < len(segs); i++ {
			if a.subtrees[maskpath.Join(segs[:i])] {
				return true
			}
		}
	}

	return false
}

func (r *maskResolver) resolve(resource proto.Message, raw []string, implied, all bool) error {
	switch {
	case implied:
		return r.implied(resource)
	case all:
		if maskpath.IsDynamicMessage(r.spec.desc) {
			if r.spec.allowed == nil {
				return r.add(maskpath.Wildcard, pathOrigin{index: SynthesizedPathIndex})
			}

			for _, path := range slices.Sorted(maps.Keys(r.spec.allowed.paths)) {
				if err := r.add(path, pathOrigin{index: SynthesizedPathIndex}); err != nil {
					return err
				}
			}

			return nil
		}

		return r.expand(r.spec.desc, "", pathOrigin{index: SynthesizedPathIndex})
	default:
		for i, path := range raw {
			if err := r.resolveExplicit(path, i); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *maskResolver) implied(resource proto.Message) error {
	s := r.spec

	policy := *s.opts
	if policy.maxPathDepth == 0 || policy.maxPathDepth > requiredMaxDepth {
		policy.maxPathDepth = requiredMaxDepth
	}

	check := updateCheck{policy: &policy, top: s.topBehavior}
	r.protected = check.impliedSelection(msgPair{req: resource.ProtoReflect()})

	for _, path := range impliedPaths(resource, slices.Concat(s.skip, policy.impliedSkipFields), &policy) {
		segs, target := s.resolutionTarget(maskpath.Split(path), 0)
		if target.message != nil {
			// Synthesis represents populated subtrees cut off by the depth
			// limit as whole paths. Resolution must not select those subtrees.
			if s.allowed.permits(path) || s.allowed.prefixes[path] {
				return fieldmask.ErrMaxPathDepthExceeded
			}

			continue
		}

		if !s.allowed.permits(path) {
			if target.field != nil && target.field.IsMap() && s.allowed.prefixes[path] {
				parent := resource.ProtoReflect()
				for _, name := range segs[:len(segs)-1] {
					parent = parent.Get(parent.Descriptor().Fields().ByName(protoreflect.Name(name))).Message()
				}

				if err := r.expandMap(target.field, path, pathOrigin{index: SynthesizedPathIndex},
					make(map[protoreflect.MessageDescriptor]bool), len(segs), populatedExpansion{message: parent, prefix: maskpath.Join(segs[:len(segs)-1])}); err != nil {
					return err
				}
			}

			continue
		}

		if err := r.add(path, pathOrigin{index: SynthesizedPathIndex}); err != nil {
			return err
		}
	}

	return nil
}

func (r *maskResolver) resolveExplicit(raw string, index int) error {
	s := r.spec

	segs, reason := resolveUpdateSegments(s.desc, raw, s.opts)
	if reason != "" {
		return indexedFieldError(fieldmask.ErrInvalidPath, index, raw, "", reason)
	}

	depth := maskpath.Depth(raw)
	segs, target := s.resolutionTarget(segs, depth)

	path := maskpath.Join(segs)
	if a := s.allowed; a != nil && !a.explicit[raw] {
		return indexedFieldError(fieldmask.ErrInvalidPath, index, raw, path, fmt.Sprintf("path %q is not allowed", raw))
	}

	if target.outputOnly || r.seen[path] {
		return nil
	}

	if r.seen == nil {
		r.seen = make(map[string]bool)
	}

	r.seen[path] = true
	r.explicit = append(r.explicit, maskPath{index: index, raw: raw, segs: segs, resolvedLeaf: target.message == nil})

	origin := pathOrigin{index: index, depth: len(segs)}

	var err error

	switch {
	case target.listDepth != 0 && s.allowed == nil:
		// The public scope keeps lists whole. The private tree keeps the
		// element selection for argument checks and protected comparisons.
		if target.listDepth < len(segs) {
			list := maskpath.Join(segs[:target.listDepth])

			if r.lists == nil {
				r.lists = make(map[string]pathOrigin)
			}

			if _, exists := r.lists[list]; !exists {
				r.lists[list] = origin
			}
		}

		err = r.add(path, origin)
	case target.message != nil:
		if target.field != nil && !target.field.IsMap() {
			r.markExpandedMessage(path, target.field, true)
		}

		err = r.expand(target.message, path, origin)
	default:
		err = r.add(path, origin)
	}

	if err != nil {
		return indexedFieldError(err, index, raw, path, err.Error())
	}

	return nil
}

type resolutionTarget struct {
	message    protoreflect.MessageDescriptor
	field      protoreflect.FieldDescriptor
	listDepth  int
	outputOnly bool
}

// resolutionTarget follows descriptor-backed fields through collection elements.
// The shared path parser drops trailing wildcards. Restore a trailing map
// wildcard for message values so validation can scope their individual fields.
func (s *Spec) resolutionTarget(segs []string, rawDepth int) ([]string, resolutionTarget) {
	md := s.desc

	var target resolutionTarget

	for i := 0; i < len(segs); i++ {
		if md == nil || maskpath.IsDynamicMessage(md) {
			break
		}

		fd := md.Fields().ByName(protoreflect.Name(segs[i]))
		if fd == nil {
			break
		}

		if s.opts.serverManaged(fd) == annotations.FieldBehavior_OUTPUT_ONLY {
			target.outputOnly = true
			break
		}

		switch {
		case fd.IsList(), fd.IsMap():
			if fd.IsList() && target.listDepth == 0 {
				target.listDepth = i + 1
			}

			md = wildcardElementDescriptor(fd, s.opts)
			if i+1 == len(segs) {
				target.field = fd
				if fd.IsMap() && md != nil && rawDepth > len(segs) {
					segs = append(segs, maskpath.Wildcard)
					target.message = md
				}

				return segs, target
			}

			i++ // map key or collection wildcard
		case descends(fd, s.opts):
			md = fd.Message()
			if i == len(segs)-1 {
				target.field = fd
			}
		default:
			md = nil
		}

		if i == len(segs)-1 && md != nil && !maskpath.IsDynamicMessage(md) {
			target.message = md
		}
	}

	return segs, target
}

func (r *maskResolver) expand(md protoreflect.MessageDescriptor, prefix string, origin pathOrigin) error {
	a := r.spec.allowed
	return r.expandFields(md, prefix, origin, make(map[protoreflect.MessageDescriptor]bool), 0, a == nil || a.subtrees[prefix], populatedExpansion{})
}

func (r *maskResolver) expandFields(md protoreflect.MessageDescriptor, prefix string, origin pathOrigin,
	active map[protoreflect.MessageDescriptor]bool, depth int, unrestricted bool, populated populatedExpansion,
) error {
	a := r.spec.allowed
	if !unrestricted && prefix != "" {
		unrestricted = a.subtrees[prefix]
		if !unrestricted && !a.prefixes[prefix] {
			return nil
		}
	}

	if populated.message == nil && unrestricted && active[md] {
		return r.add(prefix, origin)
	}

	if err := r.visitExpansion(prefix, depth); err != nil {
		return err
	}

	wasActive := active[md]

	active[md] = true
	defer func() { active[md] = wasActive }()

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if r.ignoreExpandedField(fd, prefix == "") {
			continue
		}

		path := behavior.JoinPath(prefix, string(fd.Name()))
		if err := r.expandField(fd, path, origin, active, depth+1, unrestricted, populated); err != nil {
			return err
		}
	}

	return nil
}

func (r *maskResolver) expandField(fd protoreflect.FieldDescriptor, path string, origin pathOrigin,
	active map[protoreflect.MessageDescriptor]bool, depth int, unrestricted bool, populated populatedExpansion,
) error {
	a := r.spec.allowed
	descend := descends(fd, r.spec.opts) && !maskpath.IsDynamicMessage(fd.Message())
	outputPath := path

	var child populatedExpansion

	if populated.message != nil {
		actual := populated.message.Descriptor().Fields().ByName(fd.Name())
		if actual == nil || !populated.message.Has(actual) {
			return nil
		}

		outputPath = behavior.JoinPath(populated.prefix, string(fd.Name()))
		if isImmutableBehavior(r.spec.opts.serverManaged(fd)) || slices.Contains(r.spec.opts.impliedSkipFields, outputPath) {
			return nil
		}

		if descend {
			if actual.Message() == nil || actual.IsList() || actual.IsMap() {
				return ErrMessageMismatch
			}

			child = populatedExpansion{message: populated.message.Get(actual).Message(), prefix: outputPath}
		}
	}

	switch {
	case descend:
		whole := unrestricted || a.paths[path]
		if populated.message == nil && (whole || a.prefixes[path]) {
			r.markExpandedMessage(path, fd, whole)
		}

		return r.expandFields(fd.Message(), path, origin, active, depth, unrestricted, child)
	case unrestricted || a.paths[path]:
		return r.add(outputPath, origin)
	case fd.IsMap() && a.prefixes[path]:
		return r.expandMap(fd, path, origin, active, depth, populated)
	default:
		return nil
	}
}

// populatedExpansion carries the concrete path when a wildcard is intersected
// with request values. Descriptor-only expansion leaves message nil.
type populatedExpansion struct {
	message protoreflect.Message
	prefix  string
}

func (r *maskResolver) expandMap(fd protoreflect.FieldDescriptor, path string, origin pathOrigin,
	active map[protoreflect.MessageDescriptor]bool, depth int, populated populatedExpansion,
) error {
	a := r.spec.allowed
	elem := wildcardElementDescriptor(fd, r.spec.opts)
	outputPath := path

	var values protoreflect.Map

	if populated.message != nil {
		actual := populated.message.Descriptor().Fields().ByName(fd.Name())
		if actual == nil || !actual.IsMap() || actual.MapKey().Kind() != fd.MapKey().Kind() ||
			actual.MapValue().Kind() != fd.MapValue().Kind() {
			return ErrMessageMismatch
		}

		values = populated.message.Get(actual).Map()
		outputPath = behavior.JoinPath(populated.prefix, string(fd.Name()))
	}

	for scope, key := range a.expandedMapKeys(path, fd, values) {
		scopePath := behavior.JoinPath(path, maskpath.PathSegment(scope))
		if elem == nil {
			if a.paths[scopePath] {
				if err := r.add(behavior.JoinPath(outputPath, key.segment), origin); err != nil {
					return err
				}
			}

			continue
		}

		var child populatedExpansion
		if key.value.IsValid() {
			child = populatedExpansion{message: key.value.Message(), prefix: behavior.JoinPath(outputPath, key.segment)}
		}

		if err := r.expandFields(elem, scopePath, origin, active, depth, a.subtrees[scopePath], child); err != nil {
			return err
		}
	}

	return nil
}

type mapExpansionKey struct {
	segment string
	value   protoreflect.Value
}

// expandedMapKeys yields the compiled path segment and the corresponding
// concrete segment. An omitted mask selects populated keys, and an explicit
// key in the allow-list takes precedence over the wildcard at that key.
func (a *allowedMaskPaths) expandedMapKeys(path string, fd protoreflect.FieldDescriptor, values protoreflect.Map) iter.Seq2[string, mapExpansionKey] {
	return func(yield func(string, mapExpansionKey) bool) {
		for _, segment := range a.children[path] {
			switch {
			case values == nil:
				if !yield(segment, mapExpansionKey{segment: maskpath.PathSegment(segment)}) {
					return
				}
			case segment == maskpath.Wildcard:
				for _, key := range behavior.SortedMapKeys(values) {
					concrete := maskpath.QuoteJSONKey(key.String())
					named := behavior.JoinPath(path, concrete)

					if !a.paths[named] && !a.prefixes[named] && !yield(segment, mapExpansionKey{segment: concrete, value: values.Get(key)}) {
						return
					}
				}
			default:
				key, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), segment)
				if ok && values.Has(key) && !yield(segment, mapExpansionKey{segment: maskpath.PathSegment(segment), value: values.Get(key)}) {
					return
				}
			}
		}
	}
}

func (r *maskResolver) visitExpansion(prefix string, depth int) error {
	if depth > requiredMaxDepth {
		return fieldmask.ErrMaxPathDepthExceeded
	}

	r.visits++
	if r.visits > max(r.spec.opts.maxPaths, fieldmask.DefaultMaxPaths)*requiredMaxDepth {
		return fieldmask.ErrMaxPathsExceeded
	}

	if prefix != "" {
		if v := r.spec.opts.limits().CheckPath(prefix); v != nil {
			return v.Cause
		}
	}

	return nil
}

func (r *maskResolver) markExpandedMessage(path string, fd protoreflect.FieldDescriptor, whole bool) {
	if whole && r.spec.opts.classifyField(fd, false).required {
		if r.whole == nil {
			r.whole = make(map[string]bool)
		}

		r.whole[path] = true
	}

	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		if r.oneofs == nil {
			r.oneofs = make(map[string]bool)
		}

		r.oneofs[path] = true
	}
}

func (r *maskResolver) ignoreExpandedField(fd protoreflect.FieldDescriptor, top bool) bool {
	if top {
		b, skip := r.spec.topBehavior(fd)
		return skip || b == annotations.FieldBehavior_OUTPUT_ONLY
	}

	return r.spec.opts.serverManaged(fd) == annotations.FieldBehavior_OUTPUT_ONLY
}

func (r *maskResolver) add(path string, origin pathOrigin) error {
	if previous, exists := r.origins[path]; exists {
		if origin.depth > previous.depth {
			r.origins[path] = origin
		}

		return nil
	}

	limits := r.spec.opts.limits()
	if limits.MaxPaths > 0 && len(r.origins) >= limits.MaxPaths {
		return fieldmask.ErrMaxPathsExceeded
	}

	if v := limits.CheckPath(path); v != nil {
		return v.Cause
	}

	r.origins[path] = origin

	return nil
}

func (r *maskResolver) result(implied, all bool) ResolvedMask {
	var tree fieldmask.FieldMask
	if len(r.origins) != 0 || len(r.whole) != 0 {
		tree = fieldmask.FromPaths(slices.Collect(maps.Keys(r.origins))...)
		pruneWildcardSiblings(tree)
	}

	out := ResolvedMask{
		spec: r.spec, tree: tree, whole: r.whole, oneofs: r.oneofs, explicit: r.explicit,
		protected: r.protected, implied: implied, all: all,
	}

	public := tree
	if len(r.lists) != 0 {
		public = tree.Clone()
		for path, origin := range r.lists {
			maskwalk.Insert(public, maskpath.Split(path))

			if _, exists := r.origins[path]; !exists {
				r.origins[path] = origin
			}
		}
	}

	for _, path := range declaredPaths(public, r.spec.desc) {
		out.paths = append(out.paths, ResolvedPath{Path: path, Index: r.origins[path].index})
	}

	return out
}

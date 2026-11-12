// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

// This is the a8b17ba reference order, used only by the reference walk, never
// for acceptance. Path traversal is shared with maskpath.PathBuilder; dynamic
// handling retains the base type switches and key quoting. Other helpers are
// copied verbatim with private names and relocated internal imports.

import (
	"cmp"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// Full protobuf names of the dynamic well-known types whose subtree is not
// navigable by the static field-mask schema. Callers use the predicates below
// to short-circuit recursion into such fields.
const (
	baseOrderStructFullName    protoreflect.FullName = "google.protobuf.Struct"
	baseOrderListValueFullName protoreflect.FullName = "google.protobuf.ListValue"
	baseOrderValueFullName     protoreflect.FullName = "google.protobuf.Value"
)

// baseOrderIsDynamicField returns true when fd points to one of the dynamic
// well-known types — google.protobuf.Value, Struct, or ListValue.
func baseOrderIsDynamicField(fd protoreflect.FieldDescriptor) bool {
	if fd.Kind() != protoreflect.MessageKind {
		return false
	}

	return baseOrderIsDynamicMessage(fd.Message())
}

// baseOrderIsDynamicMessage is the descriptor-keyed counterpart of
// [baseOrderIsDynamicField] used when the caller has a message descriptor in hand
// (e.g. map value descriptor or list element descriptor).
func baseOrderIsDynamicMessage(md protoreflect.MessageDescriptor) bool {
	if md == nil {
		return false
	}

	switch md.FullName() {
	case baseOrderStructFullName, baseOrderListValueFullName, baseOrderValueFullName:
		return true
	}

	return false
}

// baseOrderBuildStructPaths enumerates paths for a google.protobuf.Struct: one per
// top-level key, quoted as the path grammar requires ([behavior.QuoteSegment])
// so it round-trips through fieldmask.FromPaths. A nested Struct value is not
// descended, so the paths are stable and valid with and without
// fieldmask.WithStrictDynamicPaths. room is the number of segments the paths may
// still add to prefix (see [maskpath.PathBuilder.Build]).
func baseOrderBuildStructPaths(s *structpb.Struct, prefix string, paths *[]string, room int) {
	if room < 1 {
		return
	}

	for key := range s.GetFields() {
		*paths = append(*paths, behavior.JoinPath(prefix, behavior.QuoteSegment(key)))
	}
}

// baseOrderBuildDynamicPaths enumerates the paths of a google.protobuf.Struct,
// ListValue or Value message and reports whether msg was one of them.
func baseOrderBuildDynamicPaths(msg protoreflect.Message, prefix string, paths *[]string, room int) bool {
	switch m := msg.Interface().(type) {
	case *structpb.Struct:
		baseOrderBuildStructPaths(m, prefix, paths, room)
	case *structpb.ListValue, *structpb.Value:
		// A leaf: valid with and without WithStrictDynamicPaths.
	default:
		return false
	}

	return true
}

// baseOrderEstimatedPathCapacity is the initial capacity of the paths collected from
// a message.
const baseOrderEstimatedPathCapacity = 24

// baseOrderSetFields shapes the paths [baseOrderCollect] returns (see fieldmask.FromSetFields).
type baseOrderSetFields struct {
	// TopLevelOnly keeps only the top-level fields of the message.
	TopLevelOnly bool
	// DeclarationOrder orders the paths by the declaration order of the
	// top-level field each starts with instead of lexicographically.
	DeclarationOrder bool
	// Skip drops the named fields (paths of declared field names) and
	// everything below them, and the parents that would cover a nested one.
	Skip []string
	// Descend drops the singular message parents that would cover the named
	// nested fields, keeping the fields themselves: the mask names them, and
	// their set siblings, on their own instead of the parent whole.
	Descend []string
}

// baseOrderCollect returns the paths of every field of msg or, with onlySet, of its
// populated fields, with at most maxDepth segments (zero is unlimited),
// shaped by o and sorted: lexicographically, then with o.DeclarationOrder by
// the declaration of their top-level field.
func baseOrderCollect(msg protoreflect.Message, onlySet bool, o baseOrderSetFields, maxDepth int) []string {
	paths := make([]string, 0, baseOrderEstimatedPathCapacity)
	maskpath.PathBuilder{Dynamic: baseOrderBuildDynamicPaths}.Build(msg, "", &paths, onlySet, maskpath.DepthRoom(maxDepth))
	paths = o.keep(msg.Descriptor(), paths)

	slices.Sort(paths)

	if o.DeclarationOrder {
		baseOrderSortByDeclaration(msg.Descriptor(), paths)
	}

	return paths
}

// keep drops the paths the options exclude: below the top level with
// topLevelOnly, the skipped fields with everything below them, and the
// parents that would cover a nested skipped field (see fieldmask.FromSetFields).
func (o baseOrderSetFields) keep(md protoreflect.MessageDescriptor, paths []string) []string {
	if !o.TopLevelOnly && len(o.Skip) == 0 && len(o.Descend) == 0 {
		return paths
	}

	subtrees := slices.Clone(o.Skip) // dropped with everything below them

	parents := map[string]bool{} // dropped as exact paths only

	// addParents adds the proper prefixes of a nested path, each once: the
	// deeper ones are added only while new, so the skips of one subtree share
	// their ancestors instead of building them again.
	addParents := func(path string) {
		for i := strings.LastIndex(path, maskpath.Separator); i > 0; i = strings.LastIndex(path[:i], maskpath.Separator) {
			if parents[path[:i]] {
				return
			}

			parents[path[:i]] = true
		}
	}

	for _, descend := range o.Descend {
		if parts := strings.Split(descend, maskpath.Separator); len(parts) > 1 && !o.TopLevelOnly && baseOrderNestedSkip(md, parts) == baseOrderSkipExpandable {
			addParents(descend)
		}
	}

	for _, skip := range o.Skip {
		if !strings.Contains(skip, maskpath.Separator) {
			continue
		}

		parts := strings.Split(skip, maskpath.Separator)
		switch baseOrderNestedSkip(md, parts) {
		case baseOrderSkipExpandable:
			if o.TopLevelOnly {
				subtrees = append(subtrees, parts[0])
				continue
			}

			addParents(skip)
		case baseOrderSkipInexpressible:
			subtrees = append(subtrees, parts[0])
		case baseOrderSkipUnknown:
		}
	}

	return slices.DeleteFunc(paths, func(p string) bool {
		if o.TopLevelOnly && len(maskpath.Split(p)) > 1 {
			return true
		}

		if parents[p] {
			return true
		}

		return slices.ContainsFunc(subtrees, func(skip string) bool {
			return p == skip || strings.HasPrefix(p, skip+maskpath.Separator)
		})
	})
}

// baseOrderNestedSkipKind classifies a nested skip path against the schema.
type baseOrderNestedSkipKind int

const (
	// baseOrderSkipUnknown: the path names no field; nothing to exclude.
	baseOrderSkipUnknown baseOrderNestedSkipKind = iota
	// baseOrderSkipExpandable: every parent is a singular message, so the parents
	// can be replaced by their other set sub-paths.
	baseOrderSkipExpandable
	// baseOrderSkipInexpressible: a parent is a repeated, map or dynamic well-known
	// type field; a mask cannot select its siblings without it.
	baseOrderSkipInexpressible
)

// baseOrderNestedSkip walks the declared field names of parts from md.
func baseOrderNestedSkip(md protoreflect.MessageDescriptor, parts []string) baseOrderNestedSkipKind {
	for i, part := range parts {
		if md == nil {
			return baseOrderSkipUnknown
		}

		fd := md.Fields().ByName(protoreflect.Name(part))
		if fd == nil {
			return baseOrderSkipUnknown
		}

		if i == len(parts)-1 {
			return baseOrderSkipExpandable
		}

		switch {
		case fd.IsList() || fd.IsMap() || baseOrderIsDynamicField(fd):
			return baseOrderSkipInexpressible
		case fd.Kind() != protoreflect.MessageKind:
			return baseOrderSkipUnknown
		}

		md = fd.Message()
	}

	return baseOrderSkipUnknown
}

// baseOrderSortByDeclaration stably orders paths by the declaration index of the
// top-level field each starts with; a first segment md does not declare (a
// Struct key) sorts after the declared fields.
func baseOrderSortByDeclaration(md protoreflect.MessageDescriptor, paths []string) {
	fields := md.Fields()
	index := func(p string) int {
		parts := maskpath.Split(p)
		if len(parts) == 0 {
			return fields.Len()
		}

		if fd := fields.ByName(protoreflect.Name(parts[0])); fd != nil {
			return fd.Index()
		}

		return fields.Len()
	}

	slices.SortStableFunc(paths, func(a, b string) int { return cmp.Compare(index(a), index(b)) })
}

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskpath

import (
	"cmp"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// estimatedPathCapacity is the initial capacity of the paths collected from
// a message.
const estimatedPathCapacity = 24

// SetFields shapes the paths [Collect] returns (see fieldmask.FromSetFields).
type SetFields struct {
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

// Collect returns the paths of every field of msg or, with onlySet, of its
// populated fields, with at most maxDepth segments (zero is unlimited),
// shaped by o and sorted: lexicographically, then with o.DeclarationOrder by
// the declaration of their top-level field.
func Collect(msg protoreflect.Message, onlySet bool, o SetFields, maxDepth int) []string {
	paths := make([]string, 0, estimatedPathCapacity)
	buildPathsRecursive(msg, "", &paths, onlySet, DepthRoom(maxDepth))
	paths = o.keep(msg.Descriptor(), paths)

	slices.Sort(paths)

	if o.DeclarationOrder {
		sortByDeclaration(msg.Descriptor(), paths)
	}

	return paths
}

// keep drops the paths the options exclude: below the top level with
// topLevelOnly, the skipped fields with everything below them, and the
// parents that would cover a nested skipped field (see fieldmask.FromSetFields).
func (o SetFields) keep(md protoreflect.MessageDescriptor, paths []string) []string {
	if !o.TopLevelOnly && len(o.Skip) == 0 && len(o.Descend) == 0 {
		return paths
	}

	subtrees := slices.Clone(o.Skip) // dropped with everything below them

	var parents []string // dropped as exact paths only

	for _, descend := range o.Descend {
		if parts := strings.Split(descend, Separator); len(parts) > 1 && !o.TopLevelOnly && nestedSkip(md, parts) == skipExpandable {
			for i := 1; i < len(parts); i++ {
				parents = append(parents, strings.Join(parts[:i], Separator))
			}
		}
	}

	for _, skip := range o.Skip {
		if !strings.Contains(skip, Separator) {
			continue
		}

		parts := strings.Split(skip, Separator)
		switch nestedSkip(md, parts) {
		case skipExpandable:
			if o.TopLevelOnly {
				subtrees = append(subtrees, parts[0])
				continue
			}

			for i := 1; i < len(parts); i++ {
				parents = append(parents, strings.Join(parts[:i], Separator))
			}
		case skipInexpressible:
			subtrees = append(subtrees, parts[0])
		case skipUnknown:
		}
	}

	return slices.DeleteFunc(paths, func(p string) bool {
		if o.TopLevelOnly && len(Split(p)) > 1 {
			return true
		}

		if slices.Contains(parents, p) {
			return true
		}

		return slices.ContainsFunc(subtrees, func(skip string) bool {
			return p == skip || strings.HasPrefix(p, skip+Separator)
		})
	})
}

// nestedSkipKind classifies a nested skip path against the schema.
type nestedSkipKind int

const (
	// skipUnknown: the path names no field; nothing to exclude.
	skipUnknown nestedSkipKind = iota
	// skipExpandable: every parent is a singular message, so the parents
	// can be replaced by their other set sub-paths.
	skipExpandable
	// skipInexpressible: a parent is a repeated, map or dynamic well-known
	// type field; a mask cannot select its siblings without it.
	skipInexpressible
)

// nestedSkip walks the declared field names of parts from md.
func nestedSkip(md protoreflect.MessageDescriptor, parts []string) nestedSkipKind {
	for i, part := range parts {
		if md == nil {
			return skipUnknown
		}

		fd := md.Fields().ByName(protoreflect.Name(part))
		if fd == nil {
			return skipUnknown
		}

		if i == len(parts)-1 {
			return skipExpandable
		}

		switch {
		case fd.IsList() || fd.IsMap() || IsDynamicField(fd):
			return skipInexpressible
		case fd.Kind() != protoreflect.MessageKind:
			return skipUnknown
		}

		md = fd.Message()
	}

	return skipUnknown
}

// sortByDeclaration stably orders paths by the declaration index of the
// top-level field each starts with; a first segment md does not declare (a
// Struct key) sorts after the declared fields.
func sortByDeclaration(md protoreflect.MessageDescriptor, paths []string) {
	fields := md.Fields()
	index := func(p string) int {
		parts := Split(p)
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

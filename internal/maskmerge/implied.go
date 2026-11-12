// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// ImpliedPaths is the common populated-field selection for stripping and update
// planning. includeProtected retains protected input for comparison, selecting
// such values whole; the effective write mask omits them after validation.
// Dynamic JSON values are leaves: their keys are data, not descriptor fields.
// When supplied, top[0] overrides behaviors of root fields only.
func ImpliedPaths(m protoreflect.Message, skip []string, resolve behavior.Resolve, atomic func(protoreflect.MessageDescriptor) bool,
	maxDepth int, includeProtected bool, top ...behavior.Resolve,
) []string {
	if maskpath.IsDynamicMessage(m.Descriptor()) {
		return dynamicImpliedPaths(m, skip)
	}

	var out []string

	for _, p := range maskpath.Collect(m, true, maskpath.SetFields{DeclarationOrder: true, Skip: skip}, maxDepth) {
		leaf := impliedLeaf(m.Descriptor(), p, resolve, atomic, maxDepth, includeProtected, top)
		if leaf != "" && !slices.Contains(out, leaf) && !coversSkipped(leaf, skip) {
			out = append(out, leaf)
		}
	}

	return out
}

func coversSkipped(leaf string, skip []string) bool {
	return slices.ContainsFunc(skip, func(s string) bool { return s == leaf || strings.HasPrefix(s, leaf+maskpath.Separator) })
}

func impliedLeaf(md protoreflect.MessageDescriptor, path string, resolve behavior.Resolve, atomic func(protoreflect.MessageDescriptor) bool,
	maxDepth int, includeProtected bool, top []behavior.Resolve,
) string {
	segs := strings.Split(path, maskpath.Separator)
	for i, seg := range segs {
		fd := md.Fields().ByName(protoreflect.Name(seg))
		if fd == nil {
			return ""
		}

		b := Managed(resolve(fd))
		if i == 0 && len(top) > 0 {
			b = Managed(top[0](fd))
		}

		if b == annotations.FieldBehavior_OUTPUT_ONLY || (!includeProtected && b != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED) {
			return ""
		}

		if b != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED || fd.Message() == nil || fd.IsList() || fd.IsMap() ||
			maskpath.IsDynamicField(fd) || (atomic != nil && atomic(fd.Message())) {
			return strings.Join(segs[:i+1], maskpath.Separator)
		}

		md = fd.Message()
	}

	if maxDepth > 0 && len(segs) >= maxDepth {
		return path
	}

	return ""
}

// dynamicImpliedPaths treats JSON keys as populated fields. A scalar Value or
// populated list is indivisible; "*" is its whole-root replacement selection.
// An empty object/list or an unset Value contributes no populated field.
func dynamicImpliedPaths(m protoreflect.Message, skip []string) []string {
	switch m.Descriptor().FullName() {
	case maskpath.ValueFullName:
		fd := m.WhichOneof(m.Descriptor().Oneofs().Get(0))
		if fd == nil {
			return nil
		}

		if fd.Message() != nil {
			return dynamicImpliedPaths(m.Get(fd).Message(), skip)
		}

		return []string{maskpath.Wildcard}
	case maskpath.ListValueFullName:
		if m.Get(m.Descriptor().Fields().ByName("values")).List().Len() == 0 {
			return nil
		}

		return []string{maskpath.Wildcard}
	default:
		var out []string

		m.Get(m.Descriptor().Fields().ByName("fields")).Map().Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
			path := maskpath.QuoteJSONKey(k.String())
			if !coversSkipped(path, skip) {
				out = append(out, path)
			}

			return true
		})
		slices.Sort(out)

		return out
	}
}

// Selection is a resolved tree for callers without a public FieldMask.
type Selection map[string]Selection

// ImpliedSelection retains protected input in the shared populated selection.
func ImpliedSelection(m protoreflect.Message, resolve behavior.Resolve, atomic func(protoreflect.MessageDescriptor) bool, maxDepth int) Selection {
	out := Selection{}
	for _, path := range ImpliedPaths(m, nil, resolve, atomic, maxDepth, true) {
		// JSON keys may contain quoted separators.
		maskwalk.Insert(out, maskpath.Split(path))
	}

	return out
}

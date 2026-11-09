// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskpath

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// isFieldSet checks if a field is considered "set" (non-nil/non-zero).
func isFieldSet(msg protoreflect.Message, fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
	switch {
	case fd.IsList():
		return v.List().Len() > 0
	case fd.IsMap():
		return v.Map().Len() > 0
	case fd.HasPresence(): // For fields with optional/presence
		return msg.Has(fd)
	default: // For proto3 scalars without presence - check against zero value
		return !v.Equal(fd.Default())
	}
}

// Full protobuf names of the dynamic well-known types whose subtree is not
// navigable by the static field-mask schema. Callers use the predicates below
// to short-circuit recursion into such fields.
const (
	StructFullName    protoreflect.FullName = "google.protobuf.Struct"
	ListValueFullName protoreflect.FullName = "google.protobuf.ListValue"
	ValueFullName     protoreflect.FullName = "google.protobuf.Value"
)

// IsDynamicField returns true when fd points to one of the dynamic
// well-known types — google.protobuf.Value, Struct, or ListValue.
func IsDynamicField(fd protoreflect.FieldDescriptor) bool {
	if fd.Kind() != protoreflect.MessageKind {
		return false
	}

	return IsDynamicMessage(fd.Message())
}

// IsDynamicMessage is the descriptor-keyed counterpart of
// [IsDynamicField] used when the caller has a message descriptor in hand
// (e.g. map value descriptor or list element descriptor).
func IsDynamicMessage(md protoreflect.MessageDescriptor) bool {
	if md == nil {
		return false
	}

	switch md.FullName() {
	case StructFullName, ListValueFullName, ValueFullName:
		return true
	}

	return false
}

// buildStructPaths enumerates paths for a google.protobuf.Struct: one per
// top-level key, quoted as the path grammar requires ([behavior.QuoteSegment])
// so it round-trips through fieldmask.FromPaths. A nested Struct value is not
// descended, so the paths are stable and valid with and without
// fieldmask.WithStrictDynamicPaths. room is the number of segments the paths may
// still add to prefix (see [buildPathsRecursive]).
func buildStructPaths(s *structpb.Struct, prefix string, paths *[]string, room int) {
	if room < 1 {
		return
	}

	for key := range s.GetFields() {
		*paths = append(*paths, behavior.JoinPath(prefix, behavior.QuoteSegment(key)))
	}
}

// buildPathsRecursive builds paths for a message. If onlySetFields is true, only set fields are included.
// room is the number of segments the paths may still add to prefix under the depth limit: the walk stops
// there, so a recursive message or a deep value yields paths of at most that many segments, the deepest
// one covering what lies below it.
func buildPathsRecursive(msg protoreflect.Message, prefix string, paths *[]string, onlySetFields bool, room int) {
	if !msg.IsValid() || room < 1 {
		return
	}

	if buildDynamicPaths(msg, prefix, paths, room) {
		return
	}

	msg.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		// If onlySetFields is true, check if the field is set.
		if onlySetFields && !isFieldSet(msg, fd, v) {
			return true // Skip this field if it's not set and we only want set fields.
		}

		// Field is considered for inclusion (either set or we want all fields).
		path := behavior.JoinPath(prefix, string(fd.Name()))
		*paths = append(*paths, path) // Changed: append to *paths

		// Handle nested structures recursively
		switch {
		case fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && !fd.IsList():
			// Nested message
			buildPathsRecursive(v.Message(), path, paths, onlySetFields, room-1)
		case fd.IsMap():
			buildMapPaths(fd, v.Map(), path, paths, onlySetFields, room-1)
		}
		// A repeated field is a leaf: a path never addresses an element by
		// index (AIP-161), and an update replaces the list as a whole.

		return true
	})
}

// buildDynamicPaths enumerates the paths of a google.protobuf.Struct,
// ListValue or Value message and reports whether msg was one of them.
func buildDynamicPaths(msg protoreflect.Message, prefix string, paths *[]string, room int) bool {
	switch m := msg.Interface().(type) {
	case *structpb.Struct:
		buildStructPaths(m, prefix, paths, room)
	case *structpb.ListValue, *structpb.Value:
		// A leaf: valid with and without WithStrictDynamicPaths.
	default:
		return false
	}

	return true
}

// buildMapPaths adds a path per map key and recurses into message values.
// With onlySetFields a key is included whenever it exists — a message value
// even without set fields — except a nil message value, which is skipped.
func buildMapPaths(fd protoreflect.FieldDescriptor, m protoreflect.Map, path string, paths *[]string, onlySetFields bool, room int) {
	if room < 1 {
		return
	}

	valueIsMessage := fd.MapValue().Kind() == protoreflect.MessageKind

	m.Range(func(k protoreflect.MapKey, mapVal protoreflect.Value) bool {
		if onlySetFields && valueIsMessage && !mapVal.Message().IsValid() {
			// Skip nil message value
			return true
		}

		keyPath := path + Separator + behavior.QuoteSegment(k.String())

		*paths = append(*paths, keyPath)
		if valueIsMessage {
			buildPathsRecursive(mapVal.Message(), keyPath, paths, onlySetFields, room-1)
		}

		return true
	})
}
